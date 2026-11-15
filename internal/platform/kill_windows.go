//go:build windows

package platform

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNewProcessGroup keeps the child out of this console's Ctrl+C group.
const createNewProcessGroup = 0x00000200

// ConfigureProcess gives the child its own process group.
func ConfigureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// Terminate stops the process tree. Windows has no SIGTERM, and an adb or
// scrcpy child tree left behind would keep the local port busy, so termination
// is forceful here by design.
func Terminate(pid int) {
	taskkill(pid, true)
}

// Kill stops the process tree immediately.
func Kill(pid int) {
	taskkill(pid, true)
}

func taskkill(pid int, force bool) {
	if pid <= 0 {
		return
	}
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	cmd := exec.Command("taskkill", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

// Alive reports whether the process still exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
