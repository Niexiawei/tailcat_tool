package main

import "testing"

// A plain `go build` links no version: mygo build passes
// -X github.com/egoist/mygo.packageVersion, and a test binary has nothing.
func TestVersionTextIsDevWithoutAPackageVersion(t *testing.T) {
	if got := versionText(); got != "dev" {
		t.Fatalf("versionText() = %q, want %q for an unlinked build", got, "dev")
	}
}
