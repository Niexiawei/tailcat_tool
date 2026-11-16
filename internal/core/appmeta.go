package core

import "time"

// AppMeta is one application's identity as the device's own APK describes it.
type AppMeta struct {
	Package string
	// Label is the application's name in its own language.
	Label string
	// Icon is a picture the UI decodes, empty when the application ships only
	// a vector icon we cannot rasterize.
	Icon []byte
	// FetchedAt is when the device's APK was read.
	FetchedAt time.Time
	// Stale reports that FetchedAt is past the cache's lifetime. A stale name
	// is still shown: the old name beats the package name while a refetch runs.
	Stale bool
}

// AppMetaEntry is one cache row, for the cache page that comes later.
type AppMetaEntry struct {
	Package   string
	Label     string
	Size      int64
	FetchedAt time.Time
	Stale     bool
}

// AppMetaSource is how the manager turns a package name into what the user
// reads. Implementations do their work in the background: Lookup only ever
// answers from what is known now.
type AppMetaSource interface {
	Lookup(device, pkg string) (AppMeta, bool)
	// Request queues the packages whose entry is missing or stale and returns
	// immediately.
	Request(device, adbPath, serial string, pkgs []string)
	// Refresh queues the packages regardless of their cache state.
	Refresh(device, adbPath, serial string, pkgs []string)
}

// AppMetaCache is what the cache management page will use. It has no UI
// consumer yet; the store tests keep its shape honest.
type AppMetaCache interface {
	Entries(device string) []AppMetaEntry
	Invalidate(device, pkg string) error
	Purge(device string) error
}
