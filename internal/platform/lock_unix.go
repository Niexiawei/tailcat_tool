//go:build darwin || linux

package platform

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// FileLock is an exclusive advisory lock on a file.
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
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &FileLock{file: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
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

// Unlock releases the lock.
func (l *FileLock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// OwnedByCurrentUser reports whether the file belongs to this user.
func OwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(stat.Uid) == os.Getuid()
}
