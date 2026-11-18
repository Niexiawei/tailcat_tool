package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// derpTestMap is the smallest valid map: one region, one node.
const derpTestMap = `{"Regions":{"901":{"RegionID":901,"RegionCode":"test","Nodes":[{"Name":"901a","RegionID":901,"HostName":"relay.example.com","DERPPort":443}]}}}`

// newDerpViewModel builds a manager with one saved device, one preset and one
// device using a URL map, which is what fills the history.
func newDerpViewModel(t *testing.T) (*ViewModel, core.RelayPreset) {
	t.Helper()
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{{ID: uuid.New(), Name: "E245", RemotePort: 5555}},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	for _, profile := range m.Profiles() {
		if err := creds.Write(testAddress, profile.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "公司自建", Source: derpTestMap}); err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	if _, err := m.SaveDevice(core.SaveDeviceRequest{
		Name: "书房", Address: testAddress, RemotePort: 5555, RelaySource: "https://relay.example.com/map.json",
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDerp()
	return vm, m.RelayPresets()[0]
}

func buttonNames(tt *ui.Tester) map[string]int {
	counts := map[string]int{}
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleButton {
			counts[info.Name]++
		}
	}
	return counts
}

func TestDerpListShowsOfficialPresetsAndHistory(t *testing.T) {
	vm, _ := newDerpViewModel(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	for _, want := range []string{
		"DERP 地图",
		"官方默认",
		"官方默认节点",
		"tailcat 内置公共地图",
		"公司自建",
		"最近用过",
		"relay.example.com",
		"新建 DERP 地图",
	} {
		if !tt.HasText(want) {
			t.Fatalf("the DERP list lacks %q; texts = %v", want, tt.Texts())
		}
	}
	if tt.HasText("还没有保存的 DERP 地图") {
		t.Fatalf("the empty state shows with one preset saved; texts = %v", tt.Texts())
	}
}

func TestDerpListSaysHowManyDevicesUseEachMap(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	// The empty relay source is the official map, so E245 uses that one.
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		Name: "客厅", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if _, err := vm.Manager.SaveTemporaryDevice(core.SaveDeviceRequest{
		Name: "临时调试", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("2 台设备") {
		t.Fatalf("the preset with two devices does not say so; texts = %v", tt.Texts())
	}
	if !tt.HasText("1 台设备") {
		t.Fatalf("the official map with one device does not say so; texts = %v", tt.Texts())
	}
}

func TestOfficialRowOffersNoEditOrDelete(t *testing.T) {
	vm, _ := newDerpViewModel(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	buttons := buttonNames(tt)
	// One preset: exactly one row may offer these. The official map has no
	// local source to edit and must not pretend otherwise.
	if buttons["编辑"] != 1 {
		t.Fatalf("编辑 buttons = %d, want only the preset's; access = %v", buttons["编辑"], tt.AccessAll())
	}
	if buttons["删除"] != 1 {
		t.Fatalf("删除 buttons = %d, want only the preset's; access = %v", buttons["删除"], tt.AccessAll())
	}
	if buttons["详情"] != 1 {
		t.Fatalf("详情 buttons = %d, want only the preset's; access = %v", buttons["详情"], tt.AccessAll())
	}
}

func TestDerpListEmptyState(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDerp()
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("还没有保存的 DERP 地图") {
		t.Fatalf("no empty state without presets; texts = %v", tt.Texts())
	}
}

// newDerpURLViewModel builds a manager whose one preset points at a live test
// server, so a detail page really downloads.
func newDerpURLViewModel(t *testing.T, body string) (*ViewModel, core.RelayPreset, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{{ID: uuid.New(), Name: "E245", RemotePort: 5555}},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	if _, err := m.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "公司自建", Source: server.URL + "/map.json"}); err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	return vm, m.RelayPresets()[0], &hits
}

func TestDerpDetailShowsRegionsAndNodes(t *testing.T) {
	vm, preset, _ := newDerpURLViewModel(t, derpTestMap)
	vm.OpenDerpDetail(preset.ID)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "901a")
	for _, want := range []string{"901", "test", "relay.example.com:443", "原始 JSON"} {
		if !tt.HasText(want) {
			t.Fatalf("the detail page lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestDerpDetailShowsWhichDevicesUseIt(t *testing.T) {
	vm, preset, _ := newDerpURLViewModel(t, derpTestMap)
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		Name: "客厅", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.OpenDerpDetail(preset.ID)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "901a")
	if !tt.HasText("1 台设备在用") {
		t.Fatalf("the detail page does not name its device count; texts = %v", tt.Texts())
	}
	if !tt.HasText("客厅") {
		t.Fatalf("the detail page does not name the device; texts = %v", tt.Texts())
	}
}

func TestDerpDetailSaysThePreviewIsNotWhatDevicesUse(t *testing.T) {
	vm, preset, _ := newDerpURLViewModel(t, derpTestMap)
	vm.OpenDerpDetail(preset.ID)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "901a")
	const want = "以下内容是刚刚下载/解析的一份；设备连接时由 tailcat 自行获取，可能已变化。"
	if !tt.HasText(want) {
		t.Fatalf("the detail page does not say the preview can differ; texts = %v", tt.Texts())
	}
	if !tt.HasText("上次获取：") {
		t.Fatalf("the detail page does not say when it fetched; texts = %v", tt.Texts())
	}
}

func TestDerpForceRefetchAsksTheManagerAgain(t *testing.T) {
	vm, preset, hits := newDerpURLViewModel(t, derpTestMap)
	vm.OpenDerpDetail(preset.ID)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "901a")
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("requests = %d after opening, want 1", got)
	}
	if err := tt.Click("重新获取"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(hits) < 2 && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Fatalf("requests = %d after 重新获取, want a second real fetch", got)
	}
}

func TestDerpDetailFailureKeepsThePreset(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration:   core.Configuration{Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	if _, err := m.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "坏的", Source: server.URL + "/map.json"}); err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	preset := m.RelayPresets()[0]
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDerpDetail(preset.ID)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "重试")
	if !tt.HasText("500") {
		t.Fatalf("the failure does not carry the status code; texts = %v", tt.Texts())
	}
	if len(vm.Manager.RelayPresets()) != 1 {
		t.Fatalf("a failed fetch removed the preset; presets = %+v", vm.Manager.RelayPresets())
	}
}

func TestRelayPreviewFailureKeepsThePresetSaved(t *testing.T) {
	store := core.NewConfigStore(t.TempDir())
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Config:          store,
		Configuration:   core.Configuration{Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	// The save must not depend on reaching the server; a 500 is a fast failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	id, err := m.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "离线", Source: server.URL + "/map.json"})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDerpDetail(id)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "重试")
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.RelayPresets) != 1 || cfg.RelayPresets[0].ID != id {
		t.Fatalf("saved presets = %+v, want the failed one to stay", cfg.RelayPresets)
	}
}
