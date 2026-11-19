//go:build windows

package platform

import (
	"fmt"
	"os/exec"
)

// RevealDir opens dir in Explorer, for the startup page's buttons.
func RevealDir(dir string) error {
	if err := exec.Command("explorer", dir).Start(); err != nil {
		return fmt.Errorf("无法打开目录：%v", err)
	}
	return nil
}
