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

// DefaultAddressBookDir is the tailcat-adb CLI's state directory. The CLI is a
// Unix shell tool, so on Windows this is only where a copied address book would
// sit; it defaults to the same place a Unix home would map to.
func DefaultAddressBookDir() string {
	if env := os.Getenv("TAILCAT_ADDRESS_BOOK_DIR"); env != "" {
		return env
	}
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "tailcat-adb")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "tailcat-adb")
}
