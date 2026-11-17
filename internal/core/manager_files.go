package core

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// remoteListTimeout bounds one directory read. A large /sdcard through a relay
// takes well over a second, so this is generous on purpose.
const remoteListTimeout = 15 * time.Second

// dirKey identifies one cached listing: a device and a path on it.
type dirKey struct {
	device uuid.UUID
	path   string
}

// ListRemoteDir reads one directory off the device and caches it.
//
// It creates no task and records nothing: browsing twenty folders must not fill
// the log page or the history. Everything it runs on the device is read-only.
func (m *Manager) ListRemoteDir(ctx context.Context, id uuid.UUID, dir string) (DirListing, error) {
	// Reject an unusable path before asking the device anything.
	script, err := remoteListScript(dir)
	if err != nil {
		return DirListing{}, err
	}
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return DirListing{}, err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return DirListing{}, err
	}

	res, runErr := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", script},
		Timeout: remoteListTimeout,
	}, nil)
	if runErr != nil {
		return DirListing{}, sanitizeError(runErr)
	}
	if res.Cancelled {
		return DirListing{}, ErrCancelled
	}
	if res.TimedOut {
		return DirListing{}, Error("读取目录超时，请重试")
	}
	// 9 is the script's own code for "cd failed", which is the one failure the
	// user can act on.
	if res.ExitCode == 9 {
		return DirListing{}, Error("目录不存在或不可读：" + dir)
	}
	if err := res.RequireSuccess(); err != nil {
		return DirListing{}, sanitizeError(err)
	}
	// A directory read that outlived its channel must not land on the next one.
	if !m.targetIsCurrent(id, target) {
		return DirListing{}, ErrCancelled
	}

	listing := ParseStatOutput(res.Output, dir)
	m.mu.Lock()
	m.dirListings[dirKey{device: id, path: dir}] = listing
	m.notify()
	m.mu.Unlock()
	return listing, nil
}

// DirListing returns the cached listing of one directory, and whether it was
// read since the device connected.
func (m *Manager) DirListing(id uuid.UUID, dir string) (DirListing, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	listing, ok := m.dirListings[dirKey{device: id, path: dir}]
	return listing, ok
}

// forgetDirListingsLocked drops every cached directory of one device. m.mu must
// be held.
func (m *Manager) forgetDirListingsLocked(id uuid.UUID) {
	for key := range m.dirListings {
		if key.device == id {
			delete(m.dirListings, key)
		}
	}
}
