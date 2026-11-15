//go:build windows

package platform

import "os/exec"

// OpenTerminal opens a new console window running an adb shell.
func OpenTerminal(adbPath, serial string) error {
	cmd := exec.Command("cmd.exe", "/c", "start", "Tailcat", "cmd.exe", "/k", adbPath, "-s", serial, "shell")
	return cmd.Start()
}
