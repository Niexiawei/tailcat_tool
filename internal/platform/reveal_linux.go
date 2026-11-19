//go:build linux

package platform

import (
	"fmt"
	"os/exec"
)

// RevealDir opens dir in the desktop's file manager, for the startup page's
// buttons.
func RevealDir(dir string) error {
	if err := exec.Command("xdg-open", dir).Start(); err != nil {
		return fmt.Errorf("无法打开目录：%v", err)
	}
	return nil
}
