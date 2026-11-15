package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// failingConfigStore points at a directory whose parent is a file, so Save
// always fails after the interesting work is done.
func failingConfigStore(t *testing.T) *ConfigStore {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewConfigStore(filepath.Join(blocker, "config"))
}

func TestValidatedDeviceInput(t *testing.T) {
	cases := []struct {
		name    string
		device  string
		address string
		port    *int
		remote  int
		wantErr bool
	}{
		{"typical", "E245", sampleAddress, nil, 5555, false},
		{"empty name", "  ", sampleAddress, nil, 5555, true},
		{"long name", strings.Repeat("名", 81), sampleAddress, nil, 5555, true},
		{"bad address", "E245", "nope", nil, 5555, true},
		{"remote port zero", "E245", sampleAddress, nil, 0, true},
		{"remote port too big", "E245", sampleAddress, nil, 65536, true},
		{"preferred port zero", "E245", sampleAddress, intPtr(0), 5555, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := validatedDeviceInput(tc.device, tc.address, tc.port, tc.remote)
			if tc.wantErr && err == nil {
				t.Fatal("accepted invalid input")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("rejected valid input: %v", err)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestSaveDeviceRollbackOnPersistFailure(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = failingConfigStore(t) })
	id := uuid.New()
	_, err := f.m.SaveDevice(SaveDeviceRequest{ID: &id, Name: "car", Address: sampleAddress, RemotePort: 5555})
	if err == nil {
		t.Fatal("SaveDevice() ignored a failing configuration write")
	}
	if _, ok, _ := f.creds.Read(id); ok {
		t.Fatal("the credential survived a failed save")
	}
	if len(f.m.Profiles()) != 1 {
		t.Fatalf("profiles = %v, want the original one only", f.m.Profiles())
	}
}

func TestSaveDeviceRejectsWhileConnected(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	_, err := f.m.SaveDevice(SaveDeviceRequest{ID: &f.id, Name: "renamed", Address: sampleAddress, RemotePort: 5555})
	if err == nil {
		t.Fatal("SaveDevice() edited a live device")
	}
	if f.m.Profiles()[0].Name != "E245" {
		t.Fatal("the profile changed although the save was rejected")
	}
}

func TestSaveDeviceStoresRelaySource(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = store })
	id := uuid.New()
	got, err := f.m.SaveDevice(SaveDeviceRequest{
		ID: &id, Name: "car", Address: sampleAddress, RemotePort: 5555, RelaySource: minimalRelayJSON,
	})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var saved *Profile
	for i := range cfg.Profiles {
		if cfg.Profiles[i].ID == got {
			saved = &cfg.Profiles[i]
		}
	}
	if saved == nil {
		t.Fatalf("device %s was not persisted", got)
	}
	if !strings.HasPrefix(saved.RelaySource, "{") {
		t.Fatalf("relay source = %q, want canonical JSON", saved.RelaySource)
	}
}

func TestTemporaryDeviceMemoryOnly(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = store })
	id, err := f.m.SaveTemporaryDevice(SaveTemporaryDeviceRequest{Name: "", Address: sampleAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}
	profile, ok := findProfile(f.m.TemporaryProfiles(), id)
	if !ok {
		t.Fatal("temporary device was not registered")
	}
	if !strings.HasPrefix(profile.Name, "临时设备 ") {
		t.Fatalf("default name = %q, want a 临时设备 N label", profile.Name)
	}
	if _, ok, _ := f.creds.Read(id); ok {
		t.Fatal("a temporary device wrote to the credential store")
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Profiles {
		if p.ID == id {
			t.Fatal("a temporary device was written to configuration.json")
		}
	}
	if address, err := f.m.AddressForEditing(id); err != nil || address != sampleAddress {
		t.Fatalf("AddressForEditing() = (%q, %v)", address, err)
	}
}

func findProfile(list []Profile, id uuid.UUID) (Profile, bool) {
	for _, p := range list {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func TestPromoteTemporaryKeepsSession(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = store })
	id, err := f.m.SaveTemporaryDevice(SaveTemporaryDeviceRequest{Name: "temp", Address: sampleAddress, RemotePort: 5555})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.Connect(context.Background(), id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	serial := f.m.Session(id).Serial

	if err := f.m.PromoteTemporaryDevice(id, "保存后的名字"); err != nil {
		t.Fatalf("PromoteTemporaryDevice() error: %v", err)
	}
	if session := f.m.Session(id); session.Serial != serial || session.State != StateOnline {
		t.Fatalf("promotion disturbed the live session: %+v", session)
	}
	profile, ok := findProfile(f.m.Profiles(), id)
	if !ok || profile.Name != "保存后的名字" {
		t.Fatalf("saved profile = %+v", profile)
	}
	if _, ok := findProfile(f.m.TemporaryProfiles(), id); ok {
		t.Fatal("the device is still listed as temporary")
	}
	if _, ok, _ := f.creds.Read(id); !ok {
		t.Fatal("the credential was not moved to the credential store")
	}
	f.m.Disconnect(context.Background(), id)
}

func TestPromoteTemporaryRollsBackCredential(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = failingConfigStore(t) })
	id, err := f.m.SaveTemporaryDevice(SaveTemporaryDeviceRequest{Name: "temp", Address: sampleAddress, RemotePort: 5555})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.PromoteTemporaryDevice(id, "x"); err == nil {
		t.Fatal("PromoteTemporaryDevice() ignored a failing write")
	}
	if _, ok, _ := f.creds.Read(id); ok {
		t.Fatal("the credential was left behind after a failed promotion")
	}
	if _, ok := findProfile(f.m.TemporaryProfiles(), id); !ok {
		t.Fatal("the device stopped being temporary although the promotion failed")
	}
}

func TestRemoveDeviceDisconnectsFirst(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if err := f.m.RemoveDevice(context.Background(), f.id); err != nil {
		t.Fatalf("RemoveDevice() error: %v", err)
	}
	if f.opener.closeCount() != 1 {
		t.Fatalf("channel closes = %d, want 1", f.opener.closeCount())
	}
	if _, ok, _ := f.creds.Read(f.id); ok {
		t.Fatal("the credential of a removed device survived")
	}
	if len(f.m.Profiles()) != 0 {
		t.Fatalf("profiles = %v, want none", f.m.Profiles())
	}
}

func TestSaveRelayPresetRules(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = store })

	if _, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "  ", Source: "https://example.com/a.json"}); err == nil {
		t.Fatal("accepted an empty preset name")
	}
	if _, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "a", Source: "not json"}); err == nil {
		t.Fatal("accepted an invalid relay source")
	}
	first, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "内网", Source: minimalRelayJSON})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	if _, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "内网", Source: "https://example.com/a.json"}); err == nil {
		t.Fatal("accepted a duplicate preset name")
	}
	if _, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{ID: &first, Name: "内网2", Source: "https://example.com/b.json"}); err != nil {
		t.Fatalf("updating a preset failed: %v", err)
	}
	presets := f.m.RelayPresets()
	if len(presets) != 1 || presets[0].Name != "内网2" {
		t.Fatalf("presets = %+v", presets)
	}
}

func TestUpdateToolsRejectsActiveSessions(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if err := f.m.UpdateTools(ToolSettings{ADB: "/other/adb"}); err == nil {
		t.Fatal("UpdateTools() changed paths while a channel was live")
	}
	f.m.Disconnect(context.Background(), f.id)
	if err := f.m.UpdateTools(ToolSettings{ADB: "/other/adb"}); err != nil {
		t.Fatalf("UpdateTools() after disconnect error: %v", err)
	}
}
