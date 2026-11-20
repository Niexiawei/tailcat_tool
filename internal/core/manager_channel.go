package core

import (
	"context"
	"strings"

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
