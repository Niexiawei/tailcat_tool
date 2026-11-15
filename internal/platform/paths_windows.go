//go:build windows

package platform

import (
	"os"
	"path/filepath"
)

// appDirName is the directory this app owns in the user's roaming profile.
const appDirName = "TailcatManager"

// DefaultConfigDir is where configuration.json and Identities live.
func DefaultConfigDir() string {
	if env := os.Getenv("TAILCAT_CONFIG_DIR"); env != "" {
		return env
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, appDirName)
	}
	return filepath.Join(os.Getenv("APPDATA"), appDirName)
}
