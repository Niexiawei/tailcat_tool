//go:build linux

package platform

// PackageManager reports the package manager this build can drive, in the order
// the distros that ship them are likely to exist.
func PackageManager() (name, path string, ok bool) {
	for _, candidate := range []struct {
		name string
		bin  string
	}{
		{"apt", "apt-get"},
		{"dnf", "dnf"},
		{"pacman", "pacman"},
	} {
		if path, ok := FindTool(candidate.bin); ok {
			return candidate.name, path, true
		}
	}
	return "", "", false
}
