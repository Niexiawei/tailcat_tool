package core

import "time"

// AppMetaState is what is known about one application's name and icon right
// now. The view reads it instead of guessing from a timer: a row is busy only
// while the store says so.
type AppMetaState int

const (
	// AppMetaUnknown means nobody ever asked about this package.
	AppMetaUnknown AppMetaState = iota
	// AppMetaPending means a read is queued or running.
	AppMetaPending
	// AppMetaReady means the cache holds a name for it (possibly stale).
	AppMetaReady
	// AppMetaFailed means the read finished without a name; Reason says why.
	AppMetaFailed
)

// AppMetaStatus is one package's state plus the reason it reached it.
type AppMetaStatus struct {
	State AppMetaState
	// Reason is the user-facing sentence explaining a failure; empty otherwise.
	Reason string
	// At is when this state began, for ordering and for staleness hints.
	At time.Time
	// Final reports that retrying cannot help: the device has no APK for this
	// process at all. A batch refresh skips those.
	Final bool
}

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
	// Status reports what is known about one package right now, so the view can
	// show a spinner only while a read is really running and can say why one
	// failed.
	Status(device, pkg string) (AppMetaStatus, bool)
	// Forget drops every status this device accumulated. After a reconnect a
	// process that had no APK may be an application the user just installed.
	Forget(device string)
}

// AppMetaCache is what the cache management page will use. It has no UI
// consumer yet; the store tests keep its shape honest.
type AppMetaCache interface {
	Entries(device string) []AppMetaEntry
	Invalidate(device, pkg string) error
	Purge(device string) error
}
