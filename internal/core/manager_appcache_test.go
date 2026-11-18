package core

import (
	"strings"
	"testing"
)

// fakeAppMetaCache records what the manager asked it to drop, so one test can
// see one device's key without touching the other device's rows.
type fakeAppMetaCache struct {
	entries []AppMetaEntry
	purged  []string
	gone    []string
	err     error
}

func (f *fakeAppMetaCache) Entries(device string) []AppMetaEntry {
	if f.err != nil {
		return nil
	}
	return f.entries
}

func (f *fakeAppMetaCache) Invalidate(device, pkg string) error {
	if f.err != nil {
		return f.err
	}
	f.gone = append(f.gone, device+"|"+pkg)
	return nil
}

func (f *fakeAppMetaCache) Purge(device string) error {
	if f.err != nil {
		return f.err
	}
	f.purged = append(f.purged, device)
	return nil
}

// fakeAppMetaSource records Forget calls; the rest of the interface stays empty
// because the cache page never reads names through it.
type fakeAppMetaSource struct {
	forgotten []string
}

func (f *fakeAppMetaSource) Lookup(string, string) (AppMeta, bool)    { return AppMeta{}, false }
func (f *fakeAppMetaSource) Request(string, string, string, []string) {}
func (f *fakeAppMetaSource) Refresh(string, string, string, []string) {}
func (f *fakeAppMetaSource) Status(string, string) (AppMetaStatus, bool) {
	return AppMetaStatus{}, false
}
func (f *fakeAppMetaSource) Forget(device string) { f.forgotten = append(f.forgotten, device) }

func newCacheFixture(t *testing.T) (*managerFixture, *fakeAppMetaCache, *fakeAppMetaSource) {
	t.Helper()
	cache := &fakeAppMetaCache{entries: []AppMetaEntry{{Package: "com.tencent.mm", Label: "微信", Size: 2048}}}
	source := &fakeAppMetaSource{}
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.AppMeta = source
		o.AppMetaCache = cache
	})
	return f, cache, source
}

func TestPurgeForgetsDeviceStatus(t *testing.T) {
	f, cache, source := newCacheFixture(t)
	if err := f.m.PurgeAppMetaCache(f.id); err != nil {
		t.Fatalf("PurgeAppMetaCache() error: %v", err)
	}
	device := f.id.String()
	if len(cache.purged) != 1 || cache.purged[0] != device {
		t.Fatalf("purged = %v, want [%s]", cache.purged, device)
	}
	if len(source.forgotten) != 1 || source.forgotten[0] != device {
		t.Fatalf("forgotten = %v; the in-memory names must be dropped too", source.forgotten)
	}
}

func TestInvalidateRemovesOneEntryOnly(t *testing.T) {
	f, cache, _ := newCacheFixture(t)
	if err := f.m.InvalidateAppMetaCache(f.id, "com.tencent.mm"); err != nil {
		t.Fatalf("InvalidateAppMetaCache() error: %v", err)
	}
	want := f.id.String() + "|com.tencent.mm"
	if len(cache.gone) != 1 || cache.gone[0] != want {
		t.Fatalf("invalidated = %v, want [%s]", cache.gone, want)
	}
	if len(cache.purged) != 0 {
		t.Fatalf("invalidating one row purged the whole device: %v", cache.purged)
	}
}

func TestCacheAccessorsReportUnavailableWhenNil(t *testing.T) {
	f := newManagerFixture(t) // no AppMetaCache in the options
	if entries := f.m.AppMetaCacheEntries(f.id); entries != nil {
		t.Fatalf("entries = %+v, want nil without a cache", entries)
	}
	if err := f.m.InvalidateAppMetaCache(f.id, "com.foo"); err == nil || err.Error() != "应用信息缓存不可用" {
		t.Fatalf("Invalidate error = %v", err)
	}
	if err := f.m.PurgeAppMetaCache(f.id); err == nil || err.Error() != "应用信息缓存不可用" {
		t.Fatalf("Purge error = %v", err)
	}
}

func TestPurgeReportsAFailedCache(t *testing.T) {
	f, cache, _ := newCacheFixture(t)
	cache.err = Error("磁盘只读")
	err := f.m.PurgeAppMetaCache(f.id)
	if err == nil || !strings.Contains(err.Error(), "磁盘只读") {
		t.Fatalf("Purge error = %v, want the cache's reason", err)
	}
}
