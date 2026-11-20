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
