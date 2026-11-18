package core

import (
	"testing"

	"github.com/google/uuid"
)

// presetFixture saves one preset and returns the manager, the store and the
// preset's ID. The store lets a test check what a restart would read.
func presetFixture(t *testing.T, name string) (*managerFixture, *ConfigStore, RelayPreset) {
	t.Helper()
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Config = store
		o.Configuration = Configuration{}
	})
	id, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: name, Source: minimalRelayJSON})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	presets := f.m.RelayPresets()
	if len(presets) != 1 || presets[0].ID != id {
		t.Fatalf("presets = %+v, want the one just saved", presets)
	}
	return f, store, presets[0]
}

func TestRelayPresetUsageNamesDevices(t *testing.T) {
	f, _, preset := presetFixture(t, "公司自建")
	if _, err := f.m.SaveDevice(SaveDeviceRequest{
		Name: "客厅", Address: sampleAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if _, err := f.m.SaveTemporaryDevice(SaveDeviceRequest{
		Name: "临时调试", Address: sampleAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}

	usage := f.m.RelayPresetUsage(preset.ID)
	if len(usage) != 2 || usage[0] != "客厅" || usage[1] != "临时调试" {
		t.Fatalf("usage = %v, want [客厅 临时调试]", usage)
	}

	// A device on another source is not counted.
	if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "书房", Address: sampleAddress, RemotePort: 5555}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if usage := f.m.RelayPresetUsage(preset.ID); len(usage) != 2 {
		t.Fatalf("usage = %v, an official-map device must not count", usage)
	}
	if usage := f.m.RelayPresetUsage(uuid.New()); len(usage) != 0 {
		t.Fatalf("usage of an unknown preset = %v, want none", usage)
	}
}

func TestDeleteRelayPresetRefusesWhenADeviceUsesIt(t *testing.T) {
	f, _, preset := presetFixture(t, "公司自建")
	if _, err := f.m.SaveDevice(SaveDeviceRequest{
		Name: "客厅", Address: sampleAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if _, err := f.m.SaveTemporaryDevice(SaveDeviceRequest{
		Name: "临时调试", Address: sampleAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}

	err := f.m.DeleteRelayPreset(preset.ID)
	if err == nil {
		t.Fatal("deleted a preset two devices still use")
	}
	want := "有 2 台设备正在使用「公司自建」，请先把它们改用其他 DERP 地图"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if presets := f.m.RelayPresets(); len(presets) != 1 {
		t.Fatalf("presets = %+v, the refused delete must keep the preset", presets)
	}
}

func TestDeleteRelayPresetRemovesAnUnusedOne(t *testing.T) {
	f, store, preset := presetFixture(t, "公司自建")
	unused, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "另一张", Source: "https://example.com/map.json"})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	if _, err := f.m.SaveDevice(SaveDeviceRequest{
		Name: "客厅", Address: sampleAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}

	if err := f.m.DeleteRelayPreset(unused); err != nil {
		t.Fatalf("DeleteRelayPreset() error: %v", err)
	}
	if presets := f.m.RelayPresets(); len(presets) != 1 || presets[0].ID != preset.ID {
		t.Fatalf("presets = %+v, want only the used one", presets)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.RelayPresets) != 1 || cfg.RelayPresets[0].ID != preset.ID {
		t.Fatalf("saved presets = %+v, want the delete to be persisted", cfg.RelayPresets)
	}

	if err := f.m.DeleteRelayPreset(uuid.New()); err == nil {
		t.Fatal("deleting an unknown preset reported success")
	}
}
