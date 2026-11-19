//go:build windows

package platform

import "fmt"

// RevealDir opens dir in Explorer, for the startup page's buttons.
func RevealDir(dir string) error {
	if err := Command("explorer", dir).Start(); err != nil {
		return fmt.Errorf("无法打开目录：%v", err)
	}
	return nil
}
