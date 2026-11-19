package app

import "testing"

// The window icon comes from Environment (main embeds the same PNG mygo.json
// packages), and the view model carries it to OpenMainWindow. Without it the
// Windows taskbar and the Linux window list show a blank page.
func TestEnvironmentIconReachesTheViewModel(t *testing.T) {
	m, _ := newTestManager(t)
	a := New(Environment{
		Manager: m,
		Locate:  func(string) (string, bool) { return "", false },
		Icon:    []byte("png-bytes"),
	})
	if string(a.vm.WindowIcon) != "png-bytes" {
		t.Fatalf("vm.WindowIcon = %q, want the environment's icon", a.vm.WindowIcon)
	}
}

// No icon is not an error: the demo environment and the view tests have none,
// and OpenMainWindow simply leaves the platform default.
func TestMissingIconIsAllowed(t *testing.T) {
	m, _ := newTestManager(t)
	a := New(Environment{Manager: m, Locate: func(string) (string, bool) { return "", false }})
	if len(a.vm.WindowIcon) != 0 {
		t.Fatalf("vm.WindowIcon = %q, want none", a.vm.WindowIcon)
	}
}
