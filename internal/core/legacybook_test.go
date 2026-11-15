package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeEntry(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyBookImport(t *testing.T) {
	root := t.TempDir()
	users := filepath.Join(root, "users")
	if err := os.MkdirAll(users, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEntry(t, users, "car1", sampleAddress+"\n")
	writeEntry(t, users, "car2", "  tc"+strings.Repeat("C", 24)+"  ")
	writeEntry(t, users, "bad name", sampleAddress)
	writeEntry(t, users, "not-a-credential", "hello world")
	writeEntry(t, users, "too-big", "tc"+strings.Repeat("D", 8192))
	if err := os.MkdirAll(filepath.Join(users, "adirectory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		target := writeEntry(t, users, "target", sampleAddress)
		if err := os.Symlink(target, filepath.Join(users, "link1")); err != nil {
			t.Logf("symlink unsupported, skipping that case: %v", err)
		}
	}

	got, err := ReadLegacyBook(root)
	if err != nil {
		t.Fatalf("ReadLegacyBook() error: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("imported %d entries (%+v), want 2", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Username != "car1" || got.Entries[0].Address != sampleAddress {
		t.Fatalf("first entry = %+v", got.Entries[0])
	}
	if got.Entries[1].Username != "car2" || strings.TrimSpace(got.Entries[1].Address) != got.Entries[1].Address {
		t.Fatalf("second entry not trimmed: %+v", got.Entries[1])
	}
	if got.Skipped < 4 {
		t.Fatalf("Skipped = %d, want at least 4", got.Skipped)
	}
}

func TestLegacyBookMissingUsersDirectory(t *testing.T) {
	if _, err := ReadLegacyBook(t.TempDir()); err == nil {
		t.Fatal("ReadLegacyBook() accepted a directory without users/")
	}
}

func TestLegacyBookRejectsSymlinkedUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := t.TempDir()
	realUsers := filepath.Join(root, "real-users")
	if err := os.MkdirAll(realUsers, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEntry(t, realUsers, "car1", sampleAddress)
	if err := os.Symlink(realUsers, filepath.Join(root, "users")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := ReadLegacyBook(root); err == nil {
		t.Fatal("ReadLegacyBook() followed a symlinked users directory")
	}
}
