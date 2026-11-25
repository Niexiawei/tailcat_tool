//go:build darwin || linux

package platform

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the child in its own process group so terminating it
// also stops anything it spawned (adb server helpers, scrcpy, shells). It is
// called by Command, which is the only way this app starts a process.
func configureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// configureVisibleProcess is the same as configureProcess here: a unix child
// only shows a window when its program asks for one, so there is nothing to
// un-hide.
func configureVisibleProcess(cmd *exec.Cmd) { configureProcess(cmd) }

// Terminate asks the process group to stop.
func Terminate(pid int) {
	if pid <= 0 {
		return
	}
	// Negative pid targets the whole group; fall back to the process itself if
	// the group is already gone.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

// Kill stops the process group immediately.
func Kill(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// Alive reports whether the process still exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
