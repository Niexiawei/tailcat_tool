package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// roundTripAnswered decides what one `adb shell true` round trip proved. ADB's
// own get-state cannot answer it: it reports a cached transport state.
func TestRoundTripVerdicts(t *testing.T) {
	cases := []struct {
		name string
		res  Result
		err  error
		want bool
	}{
		{"clean exit", Result{ExitCode: 0}, nil, true},
		{"unauthorized is still an answer", Result{ExitCode: 1, Output: "error: device unauthorized\n"}, nil, true},
		{"timeout", Result{TimedOut: true, ExitCode: 1}, nil, false},
		{"cancelled", Result{Cancelled: true, ExitCode: 1}, nil, false},
		{"offline", Result{ExitCode: 1, Output: "error: device offline\n"}, nil, false},
		{"the spawn failed", Result{}, Error("无法运行 adb"), false},
	}
	for _, c := range cases {
		if got := roundTripAnswered(c.res, c.err); got != c.want {
			t.Fatalf("%s: roundTripAnswered = %v, want %v", c.name, got, c.want)
		}
	}
}

// The serial comes from the caller's snapshot, so a rebuild that swapped the
// channel mid-flight cannot make the check talk to the wrong device.
func TestRoundTripAsksOnlyTheChannelSerial(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.adbRoundTrip(context.Background(), os.Args[0], "127.0.0.1:45560"); err != nil {
		t.Fatalf("adbRoundTrip() error: %v", err)
	}
	log := f.runner.commandLog()
	if len(log) != 1 || log[0] != os.Args[0]+" -s 127.0.0.1:45560 shell true" {
		t.Fatalf("round trip ran %v, want one `-s 127.0.0.1:45560 shell true`", log)
	}
}

// connectForTest connects the fixture's device and returns the generation the
// checks are driven with, so a test can call one check directly instead of
// waiting for a ticker.
func connectForTest(t *testing.T, f *managerFixture) uuid.UUID {
	t.Helper()
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	runtime, ok := f.m.runtimes[f.id]
	if !ok {
		t.Fatal("Connect() left no runtime")
	}
	return runtime.generation
}

// sessionLogTexts flattens one session's log so a test can assert on wording.
func sessionLogTexts(session Session) []string {
	out := make([]string, 0, len(session.Logs))
	for _, line := range session.Logs {
		out = append(out, line.Text)
	}
	return out
}

// healthyADB is the fixture's default adb: the device is attached and answers.
func healthyADB(_ context.Context, c Command) (Result, error) {
	if contains(c.Args, "get-state") {
		return Result{Output: "device\n"}, nil
	}
	return Result{}, nil
}

// The fast loop only counts misses. It must never write session state: one lost
// disco packet cannot be allowed to flip the UI on its own.
func TestTunnelLoopCountsConsecutiveMisses(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = time.Hour // only the fast loop runs here
		o.TunnelInterval = 5 * time.Millisecond
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.opener.setPingErr(Error("no answer"))
	time.Sleep(40 * time.Millisecond)

	f.m.mu.Lock()
	misses := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if misses < 2 {
		t.Fatalf("tunnelMisses = %d, want at least 2", misses)
	}
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online (the fast loop must not write state)", state)
	}
	lines := strings.Join(sessionLogTexts(f.m.Session(f.id)), "\n")
	if n := strings.Count(lines, "Tailcat 链路探活连续"); n != 1 {
		t.Fatalf("the threshold was logged %d times, want 1:\n%s", n, lines)
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestTunnelProbeResetsOnAnswer(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.MonitorInterval = time.Hour })
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.mu.Lock()
	before := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if before != 2 {
		t.Fatalf("misses = %d, want 2", before)
	}

	f.opener.setPingErr(nil)
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.mu.Lock()
	after := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if after != 0 {
		t.Fatalf("misses after an answer = %d, want 0", after)
	}
}

func TestTunnelLoopStopsWithTheMonitor(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = time.Hour
		o.TunnelInterval = 5 * time.Millisecond
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.m.Disconnect(context.Background(), f.id)
	// Let a probe that was already in flight land before taking the baseline.
	time.Sleep(20 * time.Millisecond)
	before := f.opener.pingCount()
	time.Sleep(40 * time.Millisecond)
	if after := f.opener.pingCount(); after != before {
		t.Fatalf("the fast loop pinged %d more times after Disconnect", after-before)
	}
}

// A direct device has no tunnel at all: there is nothing to ping and nothing to
// spend an extra round trip on.
func TestDirectDeviceIsNeverPinged(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.TunnelInterval = 5 * time.Millisecond })
	f.m.mu.Lock()
	f.m.profiles[0].DirectADB = true
	f.m.profiles[0].DirectTarget = "emulator-5554"
	f.m.mu.Unlock()
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if n := f.opener.pingCount(); n != 0 {
		t.Fatalf("a direct device was tunnel-pinged %d times", n)
	}
	if f.opener.openCount() != 0 {
		t.Fatalf("a Tailcat channel was opened for a direct device")
	}
	f.m.Disconnect(context.Background(), f.id)
}

// silentDeviceHandler is a device whose adb lies: get-state reports the cached
// transport as "device" while a real round trip gets no answer at all.
func silentDeviceHandler(_ context.Context, c Command) (Result, error) {
	if contains(c.Args, "get-state") {
		return Result{Output: "device\n"}, nil
	}
	if contains(c.Args, "shell") {
		return Result{TimedOut: true, ExitCode: 1}, nil
	}
	return Result{}, nil
}

// quietTunnel drives the consecutive-miss counter to its threshold without
// waiting for the fast loop, so the attribution tests stay deterministic.
func quietTunnel(t *testing.T, f *managerFixture, generation uuid.UUID) {
	t.Helper()
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.probeTunnel(context.Background(), f.id, generation)
}

// Both layers silent: the tunnel is the lower one, so the device reads
// "Tailcat 断开" rather than "ADB 离线".
func TestTunnelQuietAndDeviceSilentShowsTunnelLost(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.runner.setHandler(silentDeviceHandler)
	quietTunnel(t, f, generation)

	f.m.refresh(context.Background(), f.id, generation, true)

	session := f.m.Session(f.id)
	if session.State != StateTunnelLost {
		t.Fatalf("state = %v, want %v", session.State, StateTunnelLost)
	}
	if session.Err != MsgTunnelLost {
		t.Fatalf("Err = %q, want %q", session.Err, MsgTunnelLost)
	}
}

// The tunnel probe rides DERP while data may well be going direct: a quiet
// probe with a device that still answers is a relay hiccup, not an outage.
func TestTunnelQuietButDeviceAnswersStaysOnline(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	quietTunnel(t, f, generation)
	before := len(sessionLogTexts(f.m.Session(f.id)))

	f.m.refresh(context.Background(), f.id, generation, true)

	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online", state)
	}
	if after := len(sessionLogTexts(f.m.Session(f.id))); after != before {
		t.Fatalf("the tick added %d log lines, want 0", after-before)
	}
}

// The tunnel answers, the device does not: that is a device-side outage and
// still reads "ADB 离线".
func TestStuckAdbShowsOfflineNotTunnelLost(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.runner.setHandler(silentDeviceHandler)
	before := len(sessionLogTexts(f.m.Session(f.id)))

	f.m.refresh(context.Background(), f.id, generation, true)

	session := f.m.Session(f.id)
	if session.State != StateOffline {
		t.Fatalf("state = %v, want offline", session.State)
	}
	if session.Err != MsgAdbStuck {
		t.Fatalf("Err = %q, want %q", session.Err, MsgAdbStuck)
	}
	if delta := len(sessionLogTexts(f.m.Session(f.id))) - before; delta != 1 {
		t.Fatalf("the tick logged %d lines, want 1", delta)
	}
}

// get-state keeps lying on every tick; the state must not oscillate and the log
// must not grow one line per tick (2400 lines an hour).
func TestSignalLossLogsOneLine(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.runner.setHandler(silentDeviceHandler)
	f.m.refresh(context.Background(), f.id, generation, true)
	after := len(sessionLogTexts(f.m.Session(f.id)))
	for i := 0; i < 3; i++ {
		f.m.refresh(context.Background(), f.id, generation, true)
	}
	if now := len(sessionLogTexts(f.m.Session(f.id))); now != after {
		t.Fatalf("three more ticks added %d log lines, want 0", now-after)
	}
}

// Before authorization no shell can answer, so the end-to-end check cannot run:
// a quiet tunnel alone has to be enough.
func TestTunnelLossWhileUnauthorized(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device unauthorized\n"}, nil
		}
		return Result{}, nil
	})
	generation := connectForTest(t, f)
	if state := f.m.Session(f.id).State; state != StateUnauthorized {
		t.Fatalf("state = %v, want unauthorized before the test starts", state)
	}
	f.opener.setPingErr(Error("no answer"))
	quietTunnel(t, f, generation)

	f.m.refresh(context.Background(), f.id, generation, true)

	if state := f.m.Session(f.id).State; state != StateTunnelLost {
		t.Fatalf("state = %v, want %v", state, StateTunnelLost)
	}
}

// A short outage heals on its own: the tunnel answers again, so the channel is
// not torn down and get-state decides the device's state in the same tick.
func TestTunnelRecoveryClearsTheStall(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.runner.setHandler(silentDeviceHandler)
	quietTunnel(t, f, generation)
	f.m.refresh(context.Background(), f.id, generation, true)
	if state := f.m.Session(f.id).State; state != StateTunnelLost {
		t.Fatalf("state = %v, want a stall before the test continues", state)
	}

	f.opener.setPingErr(nil)
	f.runner.setHandler(healthyADB)
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.refresh(context.Background(), f.id, generation, true)

	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online after the tunnel answered again", state)
	}
	lines := strings.Join(sessionLogTexts(f.m.Session(f.id)), "\n")
	if !strings.Contains(lines, "Tailcat 通道已恢复") {
		t.Fatalf("the recovery was not logged:\n%s", lines)
	}
}

// Every row of the attribution table in one place: this is the only function
// that decides what a tick means.
func TestChannelVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		adb      ConnectionState
		answered bool
		misses   int
		stalled  bool
		want     ConnectionState
	}{
		{"healthy", StateOnline, true, 0, false, StateOnline},
		{"device silent, tunnel fine", StateOnline, false, 0, false, StateOffline},
		{"both silent", StateOnline, false, 2, false, StateTunnelLost},
		{"relay hiccup, device answers", StateOnline, true, 2, false, StateOnline},
		{"tunnel quiet while adb offline", StateOffline, true, 2, false, StateTunnelLost},
		{"tunnel quiet while unauthorized", StateUnauthorized, true, 2, false, StateTunnelLost},
		{"unauthorized, everything fine", StateUnauthorized, true, 0, false, StateUnauthorized},
		{"offline, everything fine", StateOffline, true, 0, false, StateOffline},
		{"already stalled", StateOnline, true, 0, true, StateTunnelLost},
	}
	for _, c := range cases {
		got, _ := channelVerdict(c.adb, c.answered, c.misses, c.stalled)
		if got != c.want {
			t.Fatalf("%s: channelVerdict = %v, want %v", c.name, got, c.want)
		}
	}
}

// stallForTest drives a device into tunnelLost without timers: quiet tunnel,
// a device that does not answer, one deep tick.
func stallForTest(t *testing.T, f *managerFixture, generation uuid.UUID) {
	t.Helper()
	f.opener.setPingErr(Error("no answer"))
	f.runner.setHandler(silentDeviceHandler)
	quietTunnel(t, f, generation)
	f.m.refresh(context.Background(), f.id, generation, true)
	if state := f.m.Session(f.id).State; state != StateTunnelLost {
		t.Fatalf("state = %v, want %v before the rebuild", state, StateTunnelLost)
	}
}

// A dead tunnel cannot be fixed by adb connect: the channel has to be replaced,
// in Disconnect's order, and the monitor that noticed the stall must survive.
func TestTunnelLostRebuildsTheChannel(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	oldSerial := f.m.Session(f.id).Serial
	stallForTest(t, f, generation)

	f.opener.setPingErr(nil)
	f.runner.setHandler(healthyADB)
	f.m.retryOffline(context.Background(), f.id, generation)

	if n := f.opener.openCount(); n != 2 {
		t.Fatalf("channels opened = %d, want 2 (the dead one plus the rebuilt one)", n)
	}
	if n := f.opener.closeCount(); n != 1 {
		t.Fatalf("channels closed = %d, want 1", n)
	}
	session := f.m.Session(f.id)
	if session.State != StateOnline {
		t.Fatalf("state = %v, want online", session.State)
	}
	log := strings.Join(f.runner.commandLog(), "\n")
	if !strings.Contains(log, "disconnect "+oldSerial) {
		t.Fatalf("the dead ADB entry was not disconnected first:\n%s", log)
	}
	if !strings.Contains(log, "connect "+session.Serial) {
		t.Fatalf("the new channel was not connected:\n%s", log)
	}
	f.m.mu.Lock()
	keptGeneration := f.m.generations[f.id] == generation
	stalled := f.m.runtimes[f.id].stalled
	f.m.mu.Unlock()
	if !keptGeneration {
		t.Fatal("the rebuild changed the generation; the monitor that noticed the stall would stop")
	}
	if stalled {
		t.Fatal("the fresh channel is still marked stalled")
	}
}

// The rebuild budget is the one adb retries already use: six attempts, then the
// device says what to do instead of retrying forever.
func TestRebuildBudgetIsBounded(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	stallForTest(t, f, generation)
	f.opener.fail = Error("no channel")

	for i := 0; i < maxOfflineRetries; i++ {
		f.m.mu.Lock()
		delete(f.m.nextRetries, f.id)
		f.m.mu.Unlock()
		f.m.retryOffline(context.Background(), f.id, generation)
	}

	if n := f.opener.attemptCount(); n != 1+maxOfflineRetries {
		t.Fatalf("Open attempts = %d, want %d", n, 1+maxOfflineRetries)
	}
	session := f.m.Session(f.id)
	if session.State != StateTunnelLost {
		t.Fatalf("state = %v, want %v", session.State, StateTunnelLost)
	}
	if session.Err != MsgTunnelRebuildFailed {
		t.Fatalf("Err = %q, want %q", session.Err, MsgTunnelRebuildFailed)
	}
	// get-state keeps claiming "device"; the exhausted message must not be
	// replaced by the in-progress one on the next tick.
	f.m.refresh(context.Background(), f.id, generation, true)
	if got := f.m.Session(f.id).Err; got != MsgTunnelRebuildFailed {
		t.Fatalf("Err after another tick = %q, want %q", got, MsgTunnelRebuildFailed)
	}
}

func TestRebuildStopsWhenTheGenerationIsGone(t *testing.T) {
	f := newManagerFixture(t)
	connectForTest(t, f)
	if err := f.m.rebuildChannel(context.Background(), f.id, uuid.New()); err != nil {
		t.Fatalf("rebuildChannel() error: %v", err)
	}
	if n := f.opener.attemptCount(); n != 1 {
		t.Fatalf("Open attempts = %d, want 1 (only the original connect)", n)
	}
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want the original online", state)
	}
}

// Sampling would keep timing out against the dead channel while it is being
// rebuilt: it stops with the channel, like Disconnect does.
func TestStalledChannelStopsItsReaders(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	samplerCtx, samplerCancel := context.WithCancel(context.Background())
	f.m.mu.Lock()
	f.m.samplers[f.id] = samplerCancel
	f.m.mu.Unlock()

	stallForTest(t, f, generation)
	f.runner.setHandler(healthyADB)
	f.m.retryOffline(context.Background(), f.id, generation)

	if samplerCtx.Err() == nil {
		t.Fatal("the resource sampler kept running against the dead channel")
	}
}

// The monitor tick itself has to ask the device for a round trip: without it a
// silent device keeps reading "ADB 已连接" from adb's cache, which is the whole
// point of this feature.
func TestMonitorRunsTheEndToEndCheck(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = 10 * time.Millisecond
		o.TunnelInterval = time.Hour // only the slow loop matters here
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	f.m.Disconnect(context.Background(), f.id)

	shells := 0
	for _, cmd := range f.runner.commandLog() {
		if strings.Contains(cmd, " shell true") {
			shells++
		}
	}
	if shells < 2 {
		t.Fatalf("the monitor ran %d end-to-end checks in 60ms, want at least 2", shells)
	}
}
