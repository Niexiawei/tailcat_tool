package appmeta

import (
	"testing"
	"time"
)

func TestCacheEntryExpiresAfterSevenDays(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	c, err := newCache(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	if err := c.put("dev", "com.foo", "key", "微信", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}

	now = now.Add(TTL - time.Hour)
	meta, ok := c.lookup("dev", "com.foo")
	if !ok || meta.Stale {
		t.Fatalf("a six-day-old entry is stale: %+v, ok=%v", meta, ok)
	}

	now = now.Add(2 * time.Hour)
	meta, ok = c.lookup("dev", "com.foo")
	if !ok {
		t.Fatal("an expired entry vanished instead of going stale")
	}
	if !meta.Stale {
		t.Fatal("a seven-day-old entry is not stale")
	}
	if meta.Label != "微信" {
		t.Fatalf("the stale entry lost its label: %q", meta.Label)
	}
}

func TestCacheEntriesListsRowsForTheManagementPage(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	c, err := newCache(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	if err := c.put("dev", "com.zeta", "k1", "Zeta", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}
	if err := c.put("dev", "com.alpha", "k2", "Alpha", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}

	entries := c.entries("dev")
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Package != "com.alpha" || entries[1].Package != "com.zeta" {
		t.Fatalf("entries are not ordered by package: %+v", entries)
	}
	if entries[0].Label != "Alpha" || entries[0].Size == 0 {
		t.Fatalf("entry = %+v", entries[0])
	}
	if !entries[0].FetchedAt.Equal(now) || entries[0].Stale {
		t.Fatalf("entry = %+v", entries[0])
	}
}

func TestCacheInvalidateRemovesOneEntry(t *testing.T) {
	c, err := newCache(t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	if err := c.put("dev", "com.foo", "k1", "Foo", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}
	if err := c.put("dev", "com.bar", "k2", "Bar", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}
	if err := c.invalidate("dev", "com.foo"); err != nil {
		t.Fatalf("invalidate() error: %v", err)
	}
	if _, ok := c.lookup("dev", "com.foo"); ok {
		t.Fatal("the invalidated entry is still there")
	}
	if _, ok := c.lookup("dev", "com.bar"); !ok {
		t.Fatal("invalidate removed the wrong entry")
	}
}

func TestCachePurgeRemovesDeviceEntries(t *testing.T) {
	c, err := newCache(t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	if err := c.put("phone", "com.foo", "k1", "Foo", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}
	if err := c.put("tablet", "com.foo", "k2", "Foo", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}
	if err := c.purge("phone"); err != nil {
		t.Fatalf("purge() error: %v", err)
	}
	if _, ok := c.lookup("phone", "com.foo"); ok {
		t.Fatal("purge kept the device's entry")
	}
	if _, ok := c.lookup("tablet", "com.foo"); !ok {
		t.Fatal("purge removed another device's entry")
	}
}

func TestCacheScansDirectoryOnOpen(t *testing.T) {
	dir := t.TempDir()
	first, err := newCache(dir, time.Now)
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	if err := first.put("dev", "com.foo", "k1", "微信", testPNG); err != nil {
		t.Fatalf("put() error: %v", err)
	}

	second, err := newCache(dir, time.Now)
	if err != nil {
		t.Fatalf("reopening the cache: %v", err)
	}
	meta, ok := second.lookup("dev", "com.foo")
	if !ok || meta.Label != "微信" || len(meta.Icon) != len(testPNG) {
		t.Fatalf("reopened entry = %+v, ok=%v", meta, ok)
	}
}

func TestCachePrunesBeyondLimits(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	c, err := newCache(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("newCache() error: %v", err)
	}
	c.limit = 2
	for _, pkg := range []string{"com.old", "com.middle", "com.new"} {
		if err := c.put("dev", pkg, pkg, pkg, nil); err != nil {
			t.Fatalf("put(%s) error: %v", pkg, err)
		}
		now = now.Add(time.Minute)
	}

	entries := c.entries("dev")
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two after pruning", entries)
	}
	if entries[0].Package != "com.middle" || entries[1].Package != "com.new" {
		t.Fatalf("pruning kept the wrong entries: %+v", entries)
	}
}
