package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A configuration file with keys this build does not know (an external
// tailcat path, an address book directory, an imported username) must still
// load: unknown fields are ignored, not rejected.
const foreignConfiguration = `{
  "profiles": [
    {
      "id": "6F1C0F1A-3B7E-4C55-9F4B-2A8F0D5C1234",
      "name": "E245",
      "legacyUsername": "car1",
      "preferredPort": 5556,
      "remotePort": 5555,
      "relaySource": "https://example.com/derpmap.json"
    },
    {
      "id": "7A2D1E2B-4C8F-4D66-8E5C-3B9F1E6D2345",
      "name": "EX5",
      "remotePort": 5555
    }
  ],
  "tools": {
    "tailcat": "/opt/homebrew/bin/tailcat",
    "adb": "/opt/homebrew/bin/adb",
    "scrcpy": "/opt/homebrew/bin/scrcpy",
    "addressBookDirectory": "/Users/me/.local/state/tailcat-adb"
  }
}`

func TestConfigIgnoresUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configuration.json")
	if err := os.WriteFile(path, []byte(foreignConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewConfigStore(dir)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(cfg.Profiles))
	}
	first := cfg.Profiles[0]
	if first.Name != "E245" || first.RemotePort != 5555 {
		t.Fatalf("first profile = %+v", first)
	}
	if first.PreferredPort == nil || *first.PreferredPort != 5556 {
		t.Fatalf("first preferred port = %v", first.PreferredPort)
	}
	if cfg.Profiles[1].PreferredPort != nil {
		t.Fatalf("second preferred port should be nil, got %v", *cfg.Profiles[1].PreferredPort)
	}
	if cfg.Tools.ADB != "/opt/homebrew/bin/adb" || cfg.Tools.Scrcpy != "/opt/homebrew/bin/scrcpy" {
		t.Fatalf("tools = %+v", cfg.Tools)
	}
	if len(cfg.RelayPresets) != 0 {
		t.Fatalf("relay presets should default to empty, got %v", cfg.RelayPresets)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	port := 5557
	want := Configuration{
		Profiles: []Profile{{
			ID:            mustUUID(t, "6F1C0F1A-3B7E-4C55-9F4B-2A8F0D5C1234"),
			Name:          "E245",
			PreferredPort: &port,
			RemotePort:    5555,
			RelaySource:   "https://example.com/derpmap.json",
		}},
		Tools: ToolSettings{ADB: "/usr/bin/adb", Scrcpy: "/usr/bin/scrcpy"},
		RelayPresets: []RelayPreset{{
			ID:     mustUUID(t, "11111111-2222-3333-4444-555555555555"),
			Name:   "内网中继",
			Source: "https://example.com/derpmap.json",
		}},
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].Name != "E245" || *got.Profiles[0].PreferredPort != 5557 {
		t.Fatalf("profiles round trip = %+v", got.Profiles)
	}
	if len(got.RelayPresets) != 1 || got.RelayPresets[0].Name != "内网中继" {
		t.Fatalf("presets round trip = %+v", got.RelayPresets)
	}
	if got.Tools != want.Tools {
		t.Fatalf("tools round trip = %+v", got.Tools)
	}

	// Keys stay in the same order the Swift build wrote them, so a diff of the
	// file across builds stays readable.
	raw, err := os.ReadFile(filepath.Join(dir, "configuration.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	pi, ti, ri := strings.Index(text, `"profiles"`), strings.Index(text, `"tools"`), strings.Index(text, `"relayPresets"`)
	if pi < 0 || ti < 0 || ri < 0 || !(pi < ti && ti < ri) {
		t.Fatalf("unexpected key order in %s", text)
	}
	if strings.Contains(text, `"tailcat"`) {
		t.Fatalf("this build must not write the removed tailcat tool path: %s", text)
	}
}

func TestConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are advisory on Windows")
	}
	dir := filepath.Join(t.TempDir(), "TailcatManager")
	store := NewConfigStore(dir)
	if err := store.Save(Configuration{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("directory mode = %o, want 700", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "configuration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want 600", perm)
	}
}

func TestConfigRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte(`{"profiles":[],"tools":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("config file", func(t *testing.T) {
		dir := filepath.Join(root, "link-file")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "configuration.json")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		store := NewConfigStore(dir)
		if _, err := store.Load(); err == nil {
			t.Fatal("Load() accepted a symlinked configuration file")
		}
		if err := store.Save(Configuration{}); err == nil {
			t.Fatal("Save() accepted a symlinked configuration file")
		}
	})

	t.Run("config directory", func(t *testing.T) {
		real := filepath.Join(root, "real-dir")
		if err := os.MkdirAll(real, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link-dir")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		store := NewConfigStore(link)
		if err := store.Save(Configuration{}); err == nil {
			t.Fatal("Save() accepted a symlinked configuration directory")
		}
	})
}

func TestConfigAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	for i := 0; i < 3; i++ {
		if err := store.Save(Configuration{}); err != nil {
			t.Fatalf("Save() error: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "configuration.json" {
		t.Fatalf("directory holds %v, want only configuration.json", names)
	}
}

func TestConfigMissingFileReturnsDefaults(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() on a missing file error: %v", err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("got %d profiles, want 0", len(cfg.Profiles))
	}
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("bad uuid %q: %v", s, err)
	}
	return parsed
}

func TestSaveWritesRelayHistoryAsAnEmptyArray(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	if err := store.Save(Configuration{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"relayHistory": []`) {
		t.Fatalf("configuration.json = %s", raw)
	}
}

func TestLoadAcceptsAConfigurationWithoutRelayHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "configuration.json"), []byte(`{"profiles":[],"relayPresets":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfigStore(dir).Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.RelayHistory) != 0 {
		t.Fatalf("relayHistory = %+v, want empty", cfg.RelayHistory)
	}
}
