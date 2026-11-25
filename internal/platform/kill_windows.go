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

// configureProcess gives the child its own process group and keeps its console
// off the screen. It is called by Command, which is the only way this app
// starts a process.
func configureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup | createNoWindow
	// HideWindow covers the first frame a console would otherwise paint before
	// CREATE_NO_WINDOW takes effect.
	cmd.SysProcAttr.HideWindow = true
}

// configureVisibleProcess keeps the process group but leaves the window alone:
// the children this is for (explorer) must show the window the user asked for.
// CREATE_NO_WINDOW is ignored by GUI programs, but HideWindow is not — an
// explorer told to start hidden opens the folder without ever showing it.
func configureVisibleProcess(cmd *exec.Cmd) {
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
	// taskkill is a console program like adb: without the same flags it flashes
	// its own window, and it runs every time a device is disconnected.
	cmd := Command("taskkill", args...)
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
