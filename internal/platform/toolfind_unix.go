//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// FindTool looks an executable up in the places package managers and the
// Android SDK install into, then on PATH.
func FindTool(name string) (string, bool) {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "Library", "Android", "sdk", "platform-tools"),
		filepath.Join(home, "Android", "Sdk", "platform-tools"),
		"/usr/lib/android-sdk/platform-tools",
		"/snap/bin",
		"/usr/bin",
		"/bin",
	}
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		dirs = append(dirs, strings.Split(pathEnv, string(os.PathListSeparator))...)
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, true
		}
	}
	return "", false
}
