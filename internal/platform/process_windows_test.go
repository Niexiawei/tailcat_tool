//go:build windows

package platform

import "testing"

// The packaged app is a GUI executable with no console of its own, so a child
// that is allowed to make one gets a real window: every adb poll (once every
// three seconds, per device) flashed a cmd window on screen, in a loop, for as
// long as the app ran.
//
// This checks the flags rather than the window because a `go test` binary is a
// console program: it hands its console to every child, so the window never
// appears here even when the flags are wrong. The behavioural proof is in
// docs/verification.md (packaged app, window enumeration).
func TestCommandKeepsTheConsoleOffScreen(t *testing.T) {
	cmd := Command("cmd", "/c", "exit")
	if cmd.SysProcAttr == nil {
		t.Fatal("Command left SysProcAttr nil")
	}
	if got := cmd.SysProcAttr.CreationFlags; got&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW (%#x): the child would open a console window", got, createNoWindow)
	}
	// The group is what Terminate walks to kill an adb or scrcpy tree.
	if got := cmd.SysProcAttr.CreationFlags; got&createNewProcessGroup == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP kept", got)
	}
	// Hiding the window covers the first frame a console paints before the flag
	// above takes effect.
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow is false")
	}
}
