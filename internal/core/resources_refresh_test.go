package core

import (
	"context"
	"testing"
)

// refreshFixture wires a manager whose metadata source records refreshes.
func refreshFixture(t *testing.T) (*managerFixture, *fakeAppMeta) {
	t.Helper()
	meta := &fakeAppMeta{metas: map[string]AppMeta{}}
	f := newManagerFixture(t, func(o *ManagerOptions) { o.AppMeta = meta })
	return f, meta
}

func (f *fakeAppMeta) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refreshed...)
}

func (f *fakeAppMeta) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested, f.refreshed = nil, nil
}

func TestRefreshAppMetaRequiresOnlineDevice(t *testing.T) {
	f, meta := refreshFixture(t)
	f.serveRounds(t, defaultRound())

	if err := f.m.RefreshAppMeta(context.Background(), f.id); err == nil {
		t.Fatal("RefreshAppMeta() accepted a device with no channel")
	}
	if got := meta.snapshot(); len(got) != 0 {
		t.Fatalf("a refresh was queued for an offline device: %v", got)
	}
	for _, call := range f.runner.commandLog() {
		if containsText([]string{call}, "pm path") || containsSubstring([]string{call}, "exec-out") {
			t.Fatalf("an offline refresh touched adb: %q", call)
		}
	}
}

func TestRefreshAppMetaForcesEveryVisiblePackage(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)
	f.serveRounds(t, defaultRound())

	if _, err := f.m.SampleResources(context.Background(), f.id); err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	meta.resetCalls()

	if err := f.m.RefreshAppMeta(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshAppMeta() error: %v", err)
	}
	refreshed := meta.snapshot()
	if !containsString(refreshed, "com.foo") || !containsString(refreshed, "system_server") {
		t.Fatalf("refreshed = %v", refreshed)
	}
}

func TestRefreshAppMetaWithoutSnapshotRefreshesNothing(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)

	if err := f.m.RefreshAppMeta(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshAppMeta() error: %v", err)
	}
	if got := meta.snapshot(); len(got) != 0 {
		t.Fatalf("refreshed = %v, want nothing before the first reading", got)
	}
}
