package core

import "github.com/google/uuid"

// AppMetaCacheEntries lists what the cache holds for one device, sorted by
// package name. A missing cache answers nil rather than failing: the page shows
// that it is unavailable, which is not an error the user can act on.
func (m *Manager) AppMetaCacheEntries(id uuid.UUID) []AppMetaEntry {
	m.mu.Lock()
	cache := m.appMetaCache
	m.mu.Unlock()
	if cache == nil {
		return nil
	}
	return cache.Entries(deviceKey(id))
}

// InvalidateAppMetaCache drops one package's cached name and icon; the next
// read asks the device again.
func (m *Manager) InvalidateAppMetaCache(id uuid.UUID, pkg string) error {
	m.mu.Lock()
	cache := m.appMetaCache
	m.mu.Unlock()
	if cache == nil {
		return Error("应用信息缓存不可用")
	}
	if err := cache.Invalidate(deviceKey(id), pkg); err != nil {
		return err
	}
	m.notify()
	return nil
}

// PurgeAppMetaCache empties one device's cache and forgets its in-memory
// statuses, so the resource page reads every application again. Reads that were
// already running are discarded by the store's per-device epoch.
func (m *Manager) PurgeAppMetaCache(id uuid.UUID) error {
	m.mu.Lock()
	cache := m.appMetaCache
	source := m.appMeta
	m.mu.Unlock()
	if cache == nil {
		return Error("应用信息缓存不可用")
	}
	// The file work happens outside the manager lock: a purge can remove many
	// entries and must not stall every other manager call.
	if err := cache.Purge(deviceKey(id)); err != nil {
		return err
	}
	if source != nil {
		source.Forget(deviceKey(id))
	}
	m.notify()
	return nil
}
