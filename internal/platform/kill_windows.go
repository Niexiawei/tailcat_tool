//go:build windows

package platform

import (
	"os/exec"
	"strconv"
	"syscall"
)

const (
	// createNewProcessGroup keeps the child out of this console's Ctrl+C group.
	createNewProcessGroup = 0x00000200
	// createNoWindow gives the child no console at all.
	//
	// The packaged app is a GUI executable with no console of its own, so a
	// console child would allocate one and Windows would show it: every adb
	// poll (once every three seconds, per device) flashed a cmd window. A child
	// started from a terminal inherits that console and shows nothing, which is
	// why this only ever appeared in the built app.
	createNoWindow = 0x08000000
)

// ConfigureProcess gives the child its own process group and keeps its console
// off the screen.
func ConfigureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup | createNoWindow
	// HideWindow covers the first frame a console would otherwise paint before
	// CREATE_NO_WINDOW takes effect.
	cmd.SysProcAttr.HideWindow = true
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
	// taskkill is a console program too: without the same flags it flashes its
	// own window, and it runs every time a device is disconnected.
	ConfigureProcess(cmd)
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
