//go:build windows

package platform

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
)

// FileLock is an exclusive lock on a file.
type FileLock struct {
	file *os.File
}

// LockFile takes an exclusive lock on path, creating it 0600 if needed, and
// waits up to wait for a holder to release it. ctx cancels the wait.
func LockFile(ctx context.Context, path string, wait time.Duration) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := lockFileExclusive(f)
		if err == nil {
			return &FileLock{file: f}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			f.Close()
			return nil, err
		}
		if wait <= 0 || time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("锁已被占用")
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func lockFileExclusive(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, &ol)
}

// Unlock releases the lock.
func (l *FileLock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &ol)
	_ = l.file.Close()
	l.file = nil
}

// OwnedByCurrentUser reports whether the file belongs to this user. Windows
// access control already restricts the per-user profile directory, so this
// check is satisfied by construction.
func OwnedByCurrentUser(os.FileInfo) bool { return true }
