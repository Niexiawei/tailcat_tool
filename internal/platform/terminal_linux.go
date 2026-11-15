//go:build linux

package platform

import (
	"os/exec"
)

// OpenTerminal starts the first terminal emulator it finds with an adb shell.
func OpenTerminal(adbPath, serial string) error {
	candidates := [][]string{
		{"x-terminal-emulator", "-e"},
		{"gnome-terminal", "--"},
		{"konsole", "-e"},
		{"xfce4-terminal", "-e"},
		{"alacritty", "-e"},
		{"kitty"},
		{"foot"},
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		args := append(candidate[1:], adbPath, "-s", serial, "shell")
		if err := exec.Command(path, args...).Start(); err == nil {
			return nil
		}
	}
	return ErrorTerminalUnavailable
}
