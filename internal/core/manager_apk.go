package core

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// AppAPK is one application's installable base package as the device holds it.
type AppAPK struct {
	Path string
	// Size is the remote file's byte count; 0 when the device would not say,
	// which makes the export's progress unknown rather than zero.
	Size int64
}

// AppAPK asks the device where a package's base APK lives and how big it is.
//
// Only the base APK: an application split into several APKs answers with
// several package: lines, and bundling them back into something installable is
// a different job. The first line is the base, which is what a single-file APK
// export can honestly offer.
func (m *Manager) AppAPK(ctx context.Context, id uuid.UUID, pkg string) (AppAPK, error) {
	if !validPackageName(pkg) {
		return AppAPK{}, Error(MsgAPKPackageInvalid)
	}
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return AppAPK{}, err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return AppAPK{}, err
	}
	path, err := m.apkPath(ctx, target, pkg)
	if err != nil {
		return AppAPK{}, err
	}
	apk := AppAPK{Path: path, Size: m.apkSize(ctx, target, path)}
	if !m.targetIsCurrent(id, target) {
		return AppAPK{}, ErrCancelled
	}
	return apk, nil
}

// validPackageName guards the one place a package name is pasted into a shell
// line. Package names are [A-Za-z0-9_.]; anything else is either not a package
// name or an attempt to run a second command on the device.
func validPackageName(pkg string) bool {
	if pkg == "" || len(pkg) > 255 {
		return false
	}
	for _, r := range pkg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// apkPath asks every user's packages first: the resource page reads /proc, so
// it sees the processes of every profile, while `pm path` only answers for the
// shell's own user. `pm path` stays as the fallback for a device whose
// `pm list packages --user all` is unsupported.
func (m *Manager) apkPath(ctx context.Context, target operationTarget, pkg string) (string, error) {
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", "pm list packages --user all -f " + pkg},
		Timeout: adbStateTimeout,
	}, nil)
	if err == nil && res.ExitCode == 0 && !res.TimedOut {
		for _, raw := range strings.Split(strings.ReplaceAll(res.Output, "\r", ""), "\n") {
			line, ok := strings.CutPrefix(strings.TrimSpace(raw), "package:")
			if !ok {
				continue
			}
			// The filter matches substrings, so "com.foo" also returns
			// "com.foobar": only the exact name counts.
			found, ok := strings.CutSuffix(line, "="+pkg)
			if !ok {
				continue
			}
			if found = strings.TrimSpace(found); found != "" {
				return found, nil
			}
		}
		return "", Error(MsgAPKPathMissing)
	}

	res, err = m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", "pm path " + pkg},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil {
		return "", sanitizeError(err)
	}
	for _, raw := range strings.Split(strings.ReplaceAll(res.Output, "\r", ""), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(raw), "package:"); ok && path != "" {
			return path, nil
		}
	}
	return "", Error(MsgAPKPathMissing)
}

// apkSize is best-effort: without it the export still works and the dialog
// shows an indeterminate progress.
func (m *Manager) apkSize(ctx context.Context, target operationTarget, path string) int64 {
	if !safeRemotePath(path) {
		return 0
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", "stat -c '%s' " + path},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil || res.ExitCode != 0 {
		return 0
	}
	size, err := strconv.ParseInt(strings.TrimSpace(res.Output), 10, 64)
	if err != nil || size <= 0 {
		return 0
	}
	return size
}

// safeRemotePath keeps the path adb reported from turning into a second shell
// command. APK paths are ASCII: letters, digits and / . _ + ~ = @ - :
func safeRemotePath(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '+', r == '~', r == '=', r == '@', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}
