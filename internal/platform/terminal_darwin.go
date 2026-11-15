//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
)

// OpenTerminal opens Terminal.app running an adb shell for the serial.
func OpenTerminal(adbPath, serial string) error {
	script := "#!/bin/sh\nexec '" + adbPath + "' -s '" + serial + "' shell\n"
	file, err := os.CreateTemp("", "tailcat-shell-*.command")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(script); err != nil {
		file.Close()
		return err
	}
	file.Close()
	if err := os.Chmod(file.Name(), 0o700); err != nil {
		return err
	}
	return exec.Command("open", "-a", "Terminal", file.Name()).Start()
}

var _ = filepath.Join
