//go:build darwin

package platform

// PackageManager reports the package manager this build can drive.
func PackageManager() (name, path string, ok bool) {
	if path, ok := FindTool("brew"); ok {
		return "brew", path, true
	}
	return "", "", false
}
