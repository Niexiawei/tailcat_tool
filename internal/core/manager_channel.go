package core

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// roundTripAnswered reports whether one `adb shell true` proved the channel
// usable: a clean exit, or the device answering "unauthorized", which is still
// an answer (the tunnel and its ADB are alive, the user just has not tapped
// "allow" yet). A timeout, a spawn error or any other failure is no answer.
func roundTripAnswered(res Result, err error) bool {
	if err != nil || res.TimedOut || res.Cancelled {
		return false
	}
	if res.ExitCode == 0 {
		return true
	}
	return strings.Contains(res.Output, "unauthorized")
}

// adbRoundTrip asks the device behind the channel to answer one shell command.
// ADB's get-state cannot do this job: it reports a cached transport state and
// keeps answering "device" while the far end is silent, so only a real round
// trip tells a busy channel from a dead one.
//
// The adb path and serial are the caller's snapshot, so the check can never
// drift onto another channel mid-flight. The error is user-facing.
func (m *Manager) adbRoundTrip(ctx context.Context, adbPath, serial string) error {
	res, err := m.runner.Run(ctx, Command{
		Path:    adbPath,
		Args:    []string{"-s", serial, "shell", "true"},
		Timeout: adbRoundTripTimeout,
	}, nil)
	if roundTripAnswered(res, err) {
		return nil
	}
	return Error(MsgAdbStuck)
}

// probeTunnel runs one tunnel round trip and maintains the consecutive-miss
// counter. It never writes session state: pollLoop owns that, so one lost disco
// packet cannot flip the UI. A channel with no tunnel of its own (a direct ADB
// device, or a test's opener) has nothing to probe and returns here.
func (m *Manager) probeTunnel(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	probe := func(context.Context) error { return nil }
	if runtime != nil && runtime.conn != nil && runtime.conn.TunnelPing != nil {
		probe = runtime.conn.TunnelPing
	} else {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, tunnelPingTimeout)
	err := probe(probeCtx)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	runtime = m.runtimes[id]
	if runtime == nil || !m.isCurrentLocked(id, generation) {
		return
	}
	if err == nil {
		runtime.tunnelMisses = 0
		return
	}
	runtime.tunnelMisses++
	if runtime.tunnelMisses == tunnelMissesBeforeStalled {
		m.logLocked(id, "Tailcat 链路探活连续 2 次无响应")
		m.notify()
	}
}

// channelVerdict is the one place that decides what one tick means, so every
// row of the attribution table is one table test. It returns the state to show
// and the sentence that goes with it.
//
// tunnelMisses counts unanswered tunnel probes; two in a row mean the channel
// itself is quiet. get-state's answer is only trusted when answered is true:
// with roundTrip the caller has asked the device for a real shell round trip.
func channelVerdict(adbState ConnectionState, answered bool, tunnelMisses int, stalled bool) (ConnectionState, string) {
	if stalled {
		return StateTunnelLost, MsgTunnelLost
	}
	if tunnelMisses >= tunnelMissesBeforeStalled && (adbState != StateOnline || !answered) {
		// Both layers are silent (or the device was never reachable): the
		// tunnel is the lower one, so that is the cause worth naming.
		return StateTunnelLost, MsgTunnelLost
	}
	if adbState == StateOnline && !answered {
		return StateOffline, MsgAdbStuck
	}
	if adbState == StateOffline {
		return StateOffline, "本地通道运行中，ADB 尚未上线"
	}
	return adbState, ""
}

// openTailcatChannel builds one Tailcat channel and attaches ADB to it. The
// caller owns the generation and the monitor: this returns once ADB accepted
// the channel, or with an error the caller turns into a state. It never starts
// a monitor and never resets a generation, so the same code serves a first
// connect and a rebuild.
func (m *Manager) openTailcatChannel(ctx context.Context, id uuid.UUID, generation uuid.UUID, profile Profile, adbPath, address string) error {
	relay, err := NormalizeRelay(profile.RelaySource)
	if err != nil {
		return err
	}
	nodeKey, err := m.identities.NodeKey(ctx, id.String()+"-connection")
	if err != nil {
		return err
	}

	m.log(id, "正在建立独立 Tailcat 转发通道")
	conn, err := m.forwarder.Open(ctx, ForwardRequest{
		Address:       address,
		PreferredPort: preferredPort(profile),
		RemotePort:    remotePort(profile),
		NodeKey:       nodeKey,
		Relay:         relay,
	}, func(line string) {
		m.mu.Lock()
		current := m.isCurrentLocked(id, generation)
		m.mu.Unlock()
		if current {
			m.log(id, line)
		}
	})
	if err != nil {
		return err
	}

	m.mu.Lock()
	if !m.isCurrentLocked(id, generation) {
		m.mu.Unlock()
		_ = conn.Close(ctx)
		return nil
	}
	m.runtimes[id] = &runtimeState{conn: conn, serial: conn.Serial, adb: adbPath, generation: generation}
	m.setStateLocked(id, func(s *Session) {
		s.Serial = conn.Serial
		s.Port = conn.Port
	})
	m.notify()
	m.mu.Unlock()

	res, err := m.runner.Run(ctx, Command{
		Path:    adbPath,
		Args:    []string{"connect", conn.Serial},
		Timeout: adbConnectTimeout,
	}, nil)
	if err != nil {
		return err
	}
	if res.TimedOut {
		m.log(id, "ADB 连接等待超时，通道保留，可重试或等待设备上线")
	} else if trimmed := strings.TrimSpace(res.Output); trimmed != "" {
		m.log(id, trimmed)
	}

	for attempt := 0; attempt < connectPollAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil
		}
		m.refresh(ctx, id, generation, false)
		state := m.Session(id).State
		if state == StateOnline || state == StateUnauthorized {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.connectPoll):
		}
	}
	return nil
}

// rebuildChannel replaces a stalled channel with a fresh one, keeping the
// device's generation so the monitor that noticed the stall survives. The old
// listener and its ADB entry go first, in Disconnect's order: nothing may be
// added to adb while the ghost is still there.
func (m *Manager) rebuildChannel(ctx context.Context, id uuid.UUID, generation uuid.UUID) error {
	m.mu.Lock()
	if !m.isCurrentLocked(id, generation) {
		m.mu.Unlock()
		return nil
	}
	runtime := m.runtimes[id]
	profile, ok := m.profileLocked(id)
	var address string
	var addrErr error
	if !profile.DirectADB {
		address, addrErr = m.addressLocked(id)
	}
	m.mu.Unlock()
	if runtime == nil || !ok {
		return nil
	}

	oldSerial, adbPath, oldConn := runtime.serial, runtime.adb, runtime.conn
	if oldConn != nil {
		_ = oldConn.Close(ctx)
	}
	if oldSerial != "" {
		_, _ = m.runner.Run(ctx, Command{
			Path:    adbPath,
			Args:    []string{"disconnect", oldSerial},
			Timeout: 3 * time.Second,
		}, nil)
	}
	if addrErr != nil {
		return addrErr
	}
	if !Plausible(address) {
		return Error("设备没有有效连接凭据，请编辑设备后重试")
	}
	return m.openTailcatChannel(ctx, id, generation, profile, adbPath, address)
}
