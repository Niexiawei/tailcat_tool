package appmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
)

// TTL is how long a cached application name and icon stay valid. After it the
// entry still answers Lookup, marked stale, and the source fetches it again.
const TTL = 7 * 24 * time.Hour

const (
	// maxCacheEntries and maxCacheBytes bound the cache on disk.
	maxCacheEntries = 500
	maxCacheBytes   = 64 << 20
)

// cache is the on-disk package→(name, icon) map: one JSON file per entry, so
// a bad file costs one application rather than the whole map.
type cache struct {
	dir  string
	now  func() time.Time
	mu   sync.Mutex
	rows map[string]cacheEntry
	// epochs invalidates reads that were already running when a device's cache
	// was purged: a write whose epoch is stale is dropped instead of putting the
	// name back the user just cleared.
	epochs map[string]uint64
	// limit is maxCacheEntries, unless a test lowers it.
	limit int
}

// cacheEntry is one file's content. Device and Package live in the file so a
// scan of the directory can rebuild the index without decoding filenames.
type cacheEntry struct {
	Device    string    `json:"device"`
	Package   string    `json:"package"`
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	FetchedAt time.Time `json:"fetchedAt"`
	Icon      []byte    `json:"icon,omitempty"`
}

func entryKey(device, pkg string) string { return device + "|" + pkg }

// newCache opens (creating when needed) the cache directory and loads what it
// already holds.
func newCache(dir string, now func() time.Time) (*cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, core.Errorf("无法创建应用信息缓存目录：%v", err)
	}
	if now == nil {
		now = time.Now
	}
	c := &cache{dir: dir, now: now, rows: map[string]cacheEntry{}, epochs: map[string]uint64{}, limit: maxCacheEntries}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, core.Errorf("无法读取应用信息缓存：%v", err)
	}
	for _, file := range entries {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		var entry cacheEntry
		if err := json.Unmarshal(data, &entry); err != nil || entry.Package == "" {
			continue
		}
		c.rows[entryKey(entry.Device, entry.Package)] = entry
	}
	return c, nil
}

func (c *cache) lookup(device, pkg string) (core.AppMeta, bool) {
	return c.lookupAt(device, pkg, c.now())
}

func (c *cache) lookupAt(device, pkg string, now time.Time) (core.AppMeta, bool) {
	c.mu.Lock()
	entry, ok := c.rows[entryKey(device, pkg)]
	c.mu.Unlock()
	if !ok {
		return core.AppMeta{}, false
	}
	return core.AppMeta{
		Package:   entry.Package,
		Label:     entry.Label,
		Icon:      append([]byte(nil), entry.Icon...),
		FetchedAt: entry.FetchedAt,
		Stale:     now.Sub(entry.FetchedAt) > TTL,
	}, true
}

// fresh reports whether the entry exists, was read from the same APK and is
// still inside its lifetime.
func (c *cache) freshAt(device, pkg, key string, now time.Time) bool {
	c.mu.Lock()
	entry, ok := c.rows[entryKey(device, pkg)]
	c.mu.Unlock()
	return ok && entry.Key == key && now.Sub(entry.FetchedAt) <= TTL
}

func (c *cache) put(device, pkg, key, label string, icon []byte) error {
	stored, err := c.putAt(device, pkg, key, label, icon, c.now(), c.epochOf(device))
	if err != nil {
		return err
	}
	if !stored {
		return core.Error("应用信息缓存已被清空，本次读取被丢弃")
	}
	return nil
}

// epochOf reads the device's current purge epoch. A reader takes it before it
// starts and hands it back to putAt, which drops the result if the cache was
// cleared in between.
func (c *cache) epochOf(device string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epochs[device]
}

// putAt stores one entry unless the device's cache was purged since epoch, and
// reports whether it was stored. The epoch check and the write share the lock,
// so a purge either wins and the write is dropped, or waits and then removes
// what was just written.
func (c *cache) putAt(device, pkg, key, label string, icon []byte, now time.Time, epoch uint64) (bool, error) {
	entry := cacheEntry{
		Device:    device,
		Package:   pkg,
		Key:       key,
		Label:     label,
		FetchedAt: now,
		Icon:      icon,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return false, core.Errorf("无法编码应用信息缓存：%v", err)
	}
	path := c.path(device, pkg)
	c.mu.Lock()
	if c.epochs[device] != epoch {
		c.mu.Unlock()
		return false, nil
	}
	if err := writeFileAtomic(path, data); err != nil {
		c.mu.Unlock()
		return false, err
	}
	c.rows[entryKey(device, pkg)] = entry
	c.mu.Unlock()
	c.prune()
	return true, nil
}

func (c *cache) entries(device string) []core.AppMetaEntry {
	return c.entriesAt(device, c.now())
}

func (c *cache) entriesAt(device string, now time.Time) []core.AppMetaEntry {
	c.mu.Lock()
	rows := make([]cacheEntry, 0, len(c.rows))
	for _, entry := range c.rows {
		if entry.Device == device {
			rows = append(rows, entry)
		}
	}
	c.mu.Unlock()

	out := make([]core.AppMetaEntry, 0, len(rows))
	for _, entry := range rows {
		out = append(out, core.AppMetaEntry{
			Package:   entry.Package,
			Label:     entry.Label,
			Size:      int64(len(entry.Icon)),
			FetchedAt: entry.FetchedAt,
			Stale:     now.Sub(entry.FetchedAt) > TTL,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

func (c *cache) invalidate(device, pkg string) error {
	c.mu.Lock()
	delete(c.rows, entryKey(device, pkg))
	c.mu.Unlock()
	if err := os.Remove(c.path(device, pkg)); err != nil && !os.IsNotExist(err) {
		return core.Errorf("无法删除应用信息缓存：%v", err)
	}
	return nil
}

func (c *cache) purge(device string) error {
	c.mu.Lock()
	// Invalidating running reads comes first: a write that already holds the
	// lock finishes before this bump and is then deleted right below.
	c.epochs[device]++
	var stale []string
	for key, entry := range c.rows {
		if entry.Device == device {
			stale = append(stale, entry.Package)
			delete(c.rows, key)
		}
	}
	c.mu.Unlock()
	for _, pkg := range stale {
		if err := os.Remove(c.path(device, pkg)); err != nil && !os.IsNotExist(err) {
			return core.Errorf("无法删除应用信息缓存：%v", err)
		}
	}
	return nil
}

// prune drops the oldest entries beyond the count and byte limits.
func (c *cache) prune() {
	c.mu.Lock()
	if len(c.rows) <= c.limit && c.totalBytesLocked() <= maxCacheBytes {
		c.mu.Unlock()
		return
	}
	rows := make([]cacheEntry, 0, len(c.rows))
	for _, entry := range c.rows {
		rows = append(rows, entry)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].FetchedAt.Before(rows[j].FetchedAt) })

	var drop []cacheEntry
	total, count := c.totalBytesLocked(), len(c.rows)
	for _, entry := range rows {
		if count <= c.limit && total <= maxCacheBytes {
			break
		}
		drop = append(drop, entry)
		count--
		total -= int64(len(entry.Icon))
	}
	for _, entry := range drop {
		delete(c.rows, entryKey(entry.Device, entry.Package))
	}
	c.mu.Unlock()

	for _, entry := range drop {
		_ = os.Remove(c.path(entry.Device, entry.Package))
	}
}

func (c *cache) totalBytesLocked() int64 {
	var total int64
	for _, entry := range c.rows {
		total += int64(len(entry.Icon))
	}
	return total
}

func (c *cache) path(device, pkg string) string {
	sum := sha256.Sum256([]byte(entryKey(device, pkg)))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

// writeFileAtomic replaces path in one step and never writes through a
// symlink, so the cache cannot be pointed somewhere else.
func writeFileAtomic(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return core.Error("应用信息缓存文件是符号链接，已拒绝写入")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".appmeta-*")
	if err != nil {
		return core.Errorf("无法写入应用信息缓存：%v", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return core.Errorf("无法写入应用信息缓存：%v", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return core.Errorf("无法写入应用信息缓存：%v", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return core.Errorf("无法设置应用信息缓存权限：%v", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return core.Errorf("无法保存应用信息缓存：%v", err)
	}
	return nil
}
