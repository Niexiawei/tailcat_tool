//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// FindTool looks an executable up in the places winget, scoop, chocolatey and
// the Android SDK install into, then on PATH. It tries every PATHEXT suffix so
// "adb" resolves to adb.exe.
func FindTool(name string) (string, bool) {
	home, _ := os.UserHomeDir()
	localAppData := os.Getenv("LOCALAPPDATA")
	programData := os.Getenv("ProgramData")
	dirs := []string{
		filepath.Join(localAppData, "Android", "Sdk", "platform-tools"),
		filepath.Join(home, "scoop", "shims"),
		filepath.Join(programData, "chocolatey", "bin"),
		filepath.Join(localAppData, "Microsoft", "WinGet", "Links"),
	}
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		dirs = append(dirs, strings.Split(pathEnv, string(os.PathListSeparator))...)
	}
	exts := []string{""}
	if pathext := os.Getenv("PATHEXT"); pathext != "" {
		for _, ext := range strings.Split(pathext, string(os.PathListSeparator)) {
			if ext != "" {
				exts = append(exts, strings.ToLower(ext))
			}
		}
	} else {
		exts = append(exts, ".exe", ".cmd", ".bat")
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			candidate := filepath.Join(dir, name+ext)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate, true
			}
		}
	}
	return "", false
}
