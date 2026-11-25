//go:build darwin

package platform

import "fmt"

// RevealDir opens dir in the Finder, for the startup page's buttons.
func RevealDir(dir string) error {
	if err := VisibleCommand("open", dir).Start(); err != nil {
		return fmt.Errorf("无法打开目录：%v", err)
	}
	return nil
}
