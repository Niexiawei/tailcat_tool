package core

import (
	"context"
	"os"
	"testing"
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
