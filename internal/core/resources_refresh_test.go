package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// refreshFixture wires a manager whose metadata source records refreshes.
func refreshFixture(t *testing.T) (*managerFixture, *fakeAppMeta) {
	t.Helper()
	meta := &fakeAppMeta{metas: map[string]AppMeta{}, statuses: map[string]AppMetaStatus{}}
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

func TestResolveAppMetaRequiresOnlineDevice(t *testing.T) {
	f, meta := refreshFixture(t)
	f.serveRounds(t, defaultRound())

	if err := f.m.ResolveAppMeta(context.Background(), f.id, []string{"com.foo"}); err == nil {
		t.Fatal("ResolveAppMeta() accepted a device with no channel")
	}
	if got := meta.snapshot(); len(got) != 0 {
		t.Fatalf("a read was queued for an offline device: %v", got)
	}
	for _, call := range f.runner.commandLog() {
		if containsSubstring([]string{call}, "pm path") || containsSubstring([]string{call}, "exec-out") {
			t.Fatalf("an offline read touched adb: %q", call)
		}
	}
}

func TestResolveAppMetaQueuesOnlyTheChosenPackages(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)
	f.serveRounds(t, defaultRound())
	if _, err := f.m.SampleResources(context.Background(), f.id); err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	meta.resetCalls()

	if err := f.m.ResolveAppMeta(context.Background(), f.id, []string{"com.foo"}); err != nil {
		t.Fatalf("ResolveAppMeta() error: %v", err)
	}
	if got := meta.snapshot(); len(got) != 1 || got[0] != "com.foo" {
		t.Fatalf("refreshed = %v, want just com.foo", got)
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

func TestAppMetaStatusIsUnknownUntilAsked(t *testing.T) {
	f, _ := refreshFixture(t)

	if status, ok := f.m.AppMetaStatus(f.id, "com.foo"); ok {
		t.Fatalf("AppMetaStatus() answered %+v for an application nobody asked about", status)
	}
}

func TestAppMetaStatusIsReportedThroughTheManager(t *testing.T) {
	f, meta := refreshFixture(t)
	pending := AppMetaStatus{State: AppMetaPending, At: time.Unix(1000, 0)}
	failed := AppMetaStatus{
		State:  AppMetaFailed,
		Reason: ReasonNoAppPackage,
		Final:  true,
		At:     time.Unix(1001, 0),
	}
	meta.statuses["com.foo"] = pending
	meta.statuses["system_server"] = failed

	if status, ok := f.m.AppMetaStatus(f.id, "com.foo"); !ok || status.State != AppMetaPending {
		t.Fatalf("com.foo = %+v, %v; want the pending state back", status, ok)
	}
	if status, ok := f.m.AppMetaStatus(f.id, "system_server"); !ok || status != failed {
		t.Fatalf("system_server = %+v, %v; want the failure back verbatim", status, ok)
	}
	// The cache is keyed by the device's identity, not by its serial, so the
	// answer survives reconnects. A serial would namespace a second channel.
	for _, device := range meta.statusKeys() {
		if device != f.id.String() {
			t.Fatalf("Status() was asked about %q, want the device key %q", device, f.id.String())
		}
	}
}

func TestDisconnectForgetsAppMetaStatus(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)
	meta.statuses["system_server"] = AppMetaStatus{State: AppMetaFailed, Reason: ReasonNoAppPackage, Final: true}

	f.m.Disconnect(context.Background(), f.id)

	forgotten := meta.forgottenKeys()
	if len(forgotten) != 1 || forgotten[0] != f.id.String() {
		t.Fatalf("forgotten = %v, want exactly the device key %q", forgotten, f.id.String())
	}
}

func TestRefreshTargetsListOnlyThePackagesItWillRead(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)
	round := defaultRound()
	round.procs = append(round.procs, sampleProc{pid: 300, utime: 1, start: 800, rssKB: 2048, cmd: "android.hardware.audio.service"})
	f.serveRounds(t, round)
	if _, err := f.m.SampleResources(context.Background(), f.id); err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	// A process the device already said has no APK is not a refresh target, and
	// the page needs the same answer to report progress honestly.
	meta.statuses["android.hardware.audio.service"] = AppMetaStatus{
		State:  AppMetaFailed,
		Reason: ReasonNoAppPackage,
		Final:  true,
	}

	targets := f.m.RefreshTargets(f.id)
	if containsString(targets, "android.hardware.audio.service") {
		t.Fatalf("targets = %v; a process with no APK was listed", targets)
	}
	if !containsString(targets, "com.foo") || !containsString(targets, "system_server") {
		t.Fatalf("targets = %v; the real applications must be listed", targets)
	}
	if got := f.m.RefreshTargets(uuid.New()); len(got) != 0 {
		t.Fatalf("targets = %v for a device that was never sampled", got)
	}
}

func TestRefreshAppMetaSkipsPackagesWithNoAppPackage(t *testing.T) {
	f, meta := refreshFixture(t)
	f.connect(t)
	round := defaultRound()
	round.procs = append(round.procs, sampleProc{pid: 300, utime: 1, start: 800, rssKB: 2048, cmd: "android.hardware.audio.service"})
	f.serveRounds(t, round)
	if _, err := f.m.SampleResources(context.Background(), f.id); err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	meta.resetCalls()
	// A process the device already said has no APK cannot become an
	// application by asking again; the batch must not spend a round trip on it.
	meta.statuses["android.hardware.audio.service"] = AppMetaStatus{
		State:  AppMetaFailed,
		Reason: ReasonNoAppPackage,
		Final:  true,
	}

	if err := f.m.RefreshAppMeta(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshAppMeta() error: %v", err)
	}
	refreshed := meta.snapshot()
	if containsString(refreshed, "android.hardware.audio.service") {
		t.Fatalf("refreshed = %v; a process with no APK was asked again", refreshed)
	}
	if !containsString(refreshed, "com.foo") || !containsString(refreshed, "system_server") {
		t.Fatalf("refreshed = %v; the real applications must still be refreshed", refreshed)
	}
}
