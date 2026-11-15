//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
)

// appDirName is the directory this app owns in the user's config tree. It is
// the name the Swift build used on macOS, so an existing configuration and its
// identities are found without a migration step.
const appDirName = "TailcatManager"

// DefaultConfigDir is where configuration.json and Identities live.
func DefaultConfigDir() string {
	if env := os.Getenv("TAILCAT_CONFIG_DIR"); env != "" {
		return env
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, appDirName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", appDirName)
}

// DefaultAddressBookDir is the tailcat-adb CLI's state directory.
func DefaultAddressBookDir() string {
	if env := os.Getenv("XDG_STATE_HOME"); env != "" && filepath.IsAbs(env) {
		return filepath.Join(env, "tailcat-adb")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "tailcat-adb")
}
