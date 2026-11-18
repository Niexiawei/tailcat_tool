package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// relayTestServer serves one body and counts the requests it answered.
func relayTestServer(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func TestRelayPreviewParsesInlineMapWithoutNetwork(t *testing.T) {
	f := newManagerFixture(t)
	preview, err := f.m.RelayMapPreview(context.Background(), minimalRelayJSON)
	if err != nil {
		t.Fatalf("RelayMapPreview() error: %v", err)
	}
	if preview.Kind != RelayInline {
		t.Fatalf("Kind = %v, want inline", preview.Kind)
	}
	if preview.Map == nil || len(preview.Map.Regions) != 1 {
		t.Fatalf("Map = %+v, want one region", preview.Map)
	}
	if preview.FetchedAt.IsZero() {
		t.Fatal("FetchedAt was left zero")
	}
	if !strings.Contains(preview.JSON, `"RegionID": 901`) {
		t.Fatalf("JSON is not the canonical, indented map: %q", preview.JSON)
	}
}

func TestRelayPreviewFetchesAURL(t *testing.T) {
	server, hits := relayTestServer(t, http.StatusOK, minimalRelayJSON)
	f := newManagerFixture(t)
	preview, err := f.m.RelayMapPreview(context.Background(), server.URL+"/map.json")
	if err != nil {
		t.Fatalf("RelayMapPreview() error: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("requests = %d, want 1", *hits)
	}
	if preview.Kind != RelayURL {
		t.Fatalf("Kind = %v, want URL", preview.Kind)
	}
	if preview.Map == nil {
		t.Fatal("the downloaded map was not decoded")
	}
	region := preview.Map.Regions[901]
	if region == nil || len(region.Nodes) != 1 || region.Nodes[0].HostName != "relay.example.com" {
		t.Fatalf("regions = %+v", preview.Map.Regions)
	}
	if preview.FetchedAt.IsZero() {
		t.Fatal("FetchedAt was left zero")
	}
}

func TestRelayPreviewRejectsAnOversizedBody(t *testing.T) {
	body := strings.Repeat("x", maxRelayConfigSize+1)
	server, _ := relayTestServer(t, http.StatusOK, body)
	f := newManagerFixture(t)
	if _, err := f.m.RelayMapPreview(context.Background(), server.URL+"/big.json"); err == nil {
		t.Fatal("accepted a body larger than the relay limit")
	}
}

func TestRelayPreviewReportsHTTPFailure(t *testing.T) {
	server, _ := relayTestServer(t, http.StatusInternalServerError, minimalRelayJSON)
	f := newManagerFixture(t)
	_, err := f.m.RelayMapPreview(context.Background(), server.URL+"/map.json")
	if err == nil {
		t.Fatal("reported success for a 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %q, want the status code in it", err.Error())
	}
}

func TestRelayPreviewGivesUpAfterTheTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, minimalRelayJSON)
	}))
	defer server.Close()
	previous := relayHTTPClient
	relayHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	defer func() { relayHTTPClient = previous }()

	f := newManagerFixture(t)
	start := time.Now()
	if _, err := f.m.RelayMapPreview(context.Background(), server.URL+"/map.json"); err == nil {
		t.Fatal("reported success after the client timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waited %v, want the client timeout to end the request", elapsed)
	}
}

func TestCachedRelayPreviewNeedsNoNetwork(t *testing.T) {
	server, hits := relayTestServer(t, http.StatusOK, minimalRelayJSON)
	f := newManagerFixture(t)
	source := server.URL + "/map.json"
	if _, ok := f.m.CachedRelayMapPreview(source); ok {
		t.Fatal("answered from the cache before anything was fetched")
	}
	if _, err := f.m.RelayMapPreview(context.Background(), source); err != nil {
		t.Fatalf("RelayMapPreview() error: %v", err)
	}
	server.Close()
	preview, ok := f.m.CachedRelayMapPreview(source)
	if !ok {
		t.Fatal("the fetched map was not cached")
	}
	if preview.Map == nil || preview.Map.Regions[901].Nodes[0].HostName != "relay.example.com" {
		t.Fatalf("cached preview = %+v", preview)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("requests = %d, want the cache to answer the second time", *hits)
	}
}

func TestRelayPreviewFailureKeepsThePresetSaved(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	server, _ := relayTestServer(t, http.StatusInternalServerError, "boom")
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Config = store
		o.Configuration = Configuration{}
	})
	source := server.URL + "/map.json"
	id, err := f.m.SaveRelayPreset(SaveRelayPresetRequest{Name: "离线", Source: source})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	if _, err := f.m.RelayMapPreview(context.Background(), source); err == nil {
		t.Fatal("a 500 was reported as success")
	}
	if presets := f.m.RelayPresets(); len(presets) != 1 || presets[0].ID != id {
		t.Fatalf("presets = %+v, a failed preview must not touch them", presets)
	}
	if _, ok := f.m.CachedRelayMapPreview(source); ok {
		t.Fatal("a failed fetch was cached as if it had succeeded")
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.RelayPresets) != 1 {
		t.Fatalf("saved presets = %+v", cfg.RelayPresets)
	}
}
