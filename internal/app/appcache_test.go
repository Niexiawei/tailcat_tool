package app

import (
	"os"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// stubAppMetaCache is the page's view of the store: entries per device plus
// what the manager was asked to drop.
type stubAppMetaCache struct {
	entries map[string][]core.AppMetaEntry
	gone    []string
	purged  []string
	err     error
	// onPurge runs before Purge returns, so a test can prove a read that lands
	// in between is discarded (the store's own test covers the epoch itself).
	onPurge func()
}

func (s *stubAppMetaCache) Entries(device string) []core.AppMetaEntry {
	return s.entries[device]
}

func (s *stubAppMetaCache) Invalidate(device, pkg string) error {
	if s.err != nil {
		return s.err
	}
	s.gone = append(s.gone, device+"|"+pkg)
	delete(s.entries, device)
	return nil
}

func (s *stubAppMetaCache) Purge(device string) error {
	if s.err != nil {
		return s.err
	}
	if s.onPurge != nil {
		s.onPurge()
	}
	s.purged = append(s.purged, device)
	s.entries[device] = nil
	return nil
}

func newCacheViewModel(t *testing.T, cache core.AppMetaCache) *ViewModel {
	t.Helper()
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{{ID: uuid.New(), Name: "E245", RemotePort: 5555}},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
		AppMetaCache:    cache,
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenCache()
	return vm
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1.0 KB"},
		{1258291, "1.2 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.size); got != tc.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestCachePageListsEntriesOfChosenDevice(t *testing.T) {
	cache := &stubAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
	vm := newCacheViewModel(t, cache)
	profile := vm.Manager.Profiles()[0]
	cache.entries[profile.ID.String()] = []core.AppMetaEntry{
		{
			Package:   "com.tencent.mm",
			Label:     "微信",
			Size:      1258291,
			FetchedAt: time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local),
		},
		{
			Package:   "com.old.app",
			Label:     "旧应用",
			Size:      512,
			FetchedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local),
			Stale:     true,
		},
	}
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	for _, want := range []string{"微信", "com.tencent.mm", "1.2 MB", "2026-11-16 09:30", "已过期", "2 条"} {
		if !tt.HasText(want) {
			t.Fatalf("the cache page lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestCachePageExplainsAnEmptyCache(t *testing.T) {
	cache := &stubAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
	vm := newCacheViewModel(t, cache)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	if !tt.HasText("这台设备还没有缓存任何应用信息") {
		t.Fatalf("no empty state; texts = %v", tt.Texts())
	}
}

func TestCachePageSaysWhenTheCacheIsUnavailable(t *testing.T) {
	vm := newCacheViewModel(t, nil)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	if !tt.HasText("应用信息缓存不可用") {
		t.Fatalf("no unavailable state; texts = %v", tt.Texts())
	}
}
