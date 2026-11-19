package core

import (
	"testing"
	"time"
)

// recvSignal reports whether a signal arrives within a second.
func recvSignal(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// The window and the tray both follow the manager. One shared channel hands
// each change to whichever reader happens to receive first, so the other one
// keeps showing an older state — a tray menu stuck on "正在断开" after the
// disconnect finished, while the window had already moved on.
func TestChangedReachesEveryListener(t *testing.T) {
	f := newManagerFixture(t)
	window := f.m.Changed()
	tray := f.m.Changed()

	f.m.SetNotice("changed")

	if !recvSignal(window) {
		t.Fatal("the window's listener never heard the change")
	}
	if !recvSignal(tray) {
		t.Fatal("the tray's listener never heard the change")
	}
}

// Every listener keeps its own pending signal: a busy one cannot swallow the
// next change from a listener that happens to run later.
func TestChangedKeepsSignalsPerListener(t *testing.T) {
	f := newManagerFixture(t)
	slow := f.m.Changed()
	fast := f.m.Changed()

	// Two changes while the slow listener has not read anything: the second
	// may coalesce, but the listener still has one waiting.
	f.m.SetNotice("one")
	f.m.SetNotice("two")

	if !recvSignal(fast) {
		t.Fatal("the first listener heard nothing")
	}
	if !recvSignal(slow) {
		t.Fatal("the second listener heard nothing")
	}
	// And after draining, the next change reaches both again.
	f.m.SetNotice("three")
	if !recvSignal(fast) || !recvSignal(slow) {
		t.Fatal("a later change did not reach both listeners")
	}
}
