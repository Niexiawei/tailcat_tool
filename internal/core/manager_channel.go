package core

import (
	"context"
	"strings"
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
