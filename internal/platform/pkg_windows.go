//go:build windows

package platform

// PackageManager reports the package manager this build can drive. winget is
// the only one with a dependable Android platform-tools package.
func PackageManager() (name, path string, ok bool) {
	if path, ok := FindTool("winget"); ok {
		return "winget", path, true
	}
	return "", "", false
}
