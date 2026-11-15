package platform

import (
	"path/filepath"
	"testing"
)

func TestFindToolOnPath(t *testing.T) {
	// The Go toolchain is on PATH wherever this test runs, and it is spelled
	// the same on every platform.
	path, ok := FindTool("go")
	if !ok {
		t.Fatal("FindTool(\"go\") found nothing")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("FindTool(\"go\") = %q, want an absolute path", path)
	}
}

func TestFindToolMissing(t *testing.T) {
	if path, ok := FindTool("tailcat-definitely-not-installed-xyz"); ok {
		t.Fatalf("FindTool() found %q for a name that cannot exist", path)
	}
}

func TestDefaultDirsAreAbsolute(t *testing.T) {
	for name, dir := range map[string]string{
		"config": DefaultConfigDir(),
	} {
		if !filepath.IsAbs(dir) {
			t.Fatalf("%s dir = %q, want an absolute path", name, dir)
		}
	}
}
