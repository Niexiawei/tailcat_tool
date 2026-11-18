package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// openDerpEditorFor opens the list and clicks the button with that name.
func clickInList(t *testing.T, vm *ViewModel, name string) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click(name); err != nil {
		t.Fatalf("Click(%q) error: %v", name, err)
	}
	tt.Frame()
	return tt
}

// focusField puts the caret in the text field with that accessible name. The
// visible label is a separate text, so Click would hit the label instead.
func focusField(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleTextField, name)
	if !ok {
		t.Fatalf("text field %q not found; access = %v", name, tt.AccessAll())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
}

func TestDerpEditorSavesAnInlineMap(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDerp()
	tt := clickInList(t, vm, "新建 DERP 地图")
	if !tt.HasText("新建 DERP 地图") {
		t.Fatalf("the editor did not open; texts = %v", tt.Texts())
	}
	focusField(t, tt, "名称")
	tt.Type("公司自建")
	focusField(t, tt, "DERP 地图 JSON")
	tt.Type(derpTestMap)
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	presets := vm.Manager.RelayPresets()
	if len(presets) != 1 || presets[0].Name != "公司自建" {
		t.Fatalf("presets = %+v, want the saved one", presets)
	}
	if vm.DerpEditor != nil {
		t.Fatal("the editor stayed open after a successful save")
	}
	tt.Frame()
	if !tt.HasText("公司自建") {
		t.Fatalf("the saved preset does not show in the list; texts = %v", tt.Texts())
	}
}

func TestDerpEditorRejectsInvalidJSON(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDerp()
	tt := clickInList(t, vm, "新建 DERP 地图")
	focusField(t, tt, "名称")
	tt.Type("坏的")
	focusField(t, tt, "DERP 地图 JSON")
	tt.Type(`{"Regions":{}}`)
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.DerpEditor == nil {
		t.Fatal("the editor closed after an invalid map")
	}
	if !tt.HasText("1–128 个 Regions") {
		t.Fatalf("the editor shows no reason; texts = %v", tt.Texts())
	}
	if len(vm.Manager.RelayPresets()) != 0 {
		t.Fatalf("presets = %+v, want an invalid map rejected", vm.Manager.RelayPresets())
	}
}

func TestDerpEditorWarnsWhenThePresetIsInUse(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		Name: "客厅", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.OpenDerp()
	if err := ui.NewTester(vm.ContentView, 1000, 800).Click("编辑"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	const want = "这台地图正被 1 台设备使用。修改只影响之后选择它的设备；已保存的设备仍用原来的来源。"
	if !tt.HasText(want) {
		t.Fatalf("editing a preset in use does not warn; texts = %v", tt.Texts())
	}
}

func TestDerpDeleteAsksBeforeRemovingAPreset(t *testing.T) {
	vm := newTestViewModel(t)
	const source = "https://relay.example.com/map.json"
	if _, err := vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "公司自建", Source: source}); err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	vm.OpenDerp()
	tt := clickInList(t, vm, "删除")
	if !tt.HasText("删除「公司自建」？") {
		t.Fatalf("the delete confirmation did not open; texts = %v", tt.Texts())
	}
	if len(vm.Manager.RelayPresets()) != 1 {
		t.Fatal("the preset was deleted before the confirmation")
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if len(vm.Manager.RelayPresets()) != 0 {
		t.Fatalf("presets = %+v, want the confirmed one gone", vm.Manager.RelayPresets())
	}
	if vm.DerpConfirmDelete != nil {
		t.Fatal("the confirmation stayed open after the delete")
	}
}

func TestDerpDeleteRefusalIsShownInTheDialog(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		Name: "客厅", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.OpenDerp()
	tt := clickInList(t, vm, "删除")
	if err := tt.Click("确认删除"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.DerpConfirmDelete == nil {
		t.Fatal("the dialog closed instead of showing the refusal")
	}
	if !tt.HasText("有 1 台设备正在使用「公司自建」，请先把它们改用其他 DERP 地图") {
		t.Fatalf("the refusal is not shown in the dialog; texts = %v", tt.Texts())
	}
	if len(vm.Manager.RelayPresets()) != 1 {
		t.Fatalf("presets = %+v, want the used one kept", vm.Manager.RelayPresets())
	}
}

// TestDerpSourceInfoIsParsedOnce pins the list's cache: a 128 KiB inline map
// must not be parsed and re-marshalled on every frame.
func TestDerpSourceInfoIsParsedOnce(t *testing.T) {
	vm := newTestViewModel(t)
	if info := vm.derpInfoFor(derpTestMap); info.summary != "1 个区域 · 1 个节点" {
		t.Fatalf("summary = %q", info.summary)
	}
	// Replacing the entry proves the second call read the cache, not the map.
	vm.derpInfo[derpTestMap] = derpSourceInfo{kind: core.RelayURL, summary: "sentinel"}
	if info := vm.derpInfoFor(derpTestMap); info.summary != "sentinel" {
		t.Fatalf("info = %+v, want the cached entry", info)
	}
}

// TestEnsureDerpPreviewReusesTheCachedCopy pins that a frame does not ask core
// to parse the same inline map again.
func TestEnsureDerpPreviewReusesTheCachedCopy(t *testing.T) {
	vm := newTestViewModel(t)
	fetched := time.Unix(1700000000, 0)
	vm.derpPreviews = map[string]core.RelayMapPreview{
		derpTestMap: {Source: derpTestMap, Kind: core.RelayInline, FetchedAt: fetched},
	}
	vm.ensureDerpPreview(core.RelayPreset{Name: "公司自建", Source: derpTestMap})
	if vm.DerpPreview == nil || !vm.DerpPreview.FetchedAt.Equal(fetched) {
		t.Fatalf("preview = %+v, want the cached copy without a new fetch", vm.DerpPreview)
	}
	if vm.DerpPreviewLoading {
		t.Fatal("a cached copy started a fetch")
	}
}

// manyRegionMap builds a valid map with count regions, for the long-page case.
func manyRegionMap(count int) string {
	var b strings.Builder
	b.WriteString(`{"Regions":{`)
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%d":{"RegionID":%d,"RegionCode":"r%d","Nodes":[{"Name":"n%d","RegionID":%d,"HostName":"relay%d.example.com","DERPPort":443}]}`, i, i, i, i, i, i)
	}
	b.WriteString(`}}`)
	return b.String()
}

// TestDerpDetailScrollsLongMaps pins that a 60-region map is reachable: the
// page content has to scroll, not spill past the window.
func TestDerpDetailScrollsLongMaps(t *testing.T) {
	vm := newTestViewModel(t)
	id, err := vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "大国", Source: manyRegionMap(60)})
	if err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	vm.OpenDerpDetail(id)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "relay60.example.com")
	scrolled := false
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleScroll {
			scrolled = true
		}
	}
	if !scrolled {
		t.Fatalf("the detail page has no scroll area; access = %v", tt.AccessAll())
	}
}

// TestDerpEditorClosesWithoutPanic pins the crash the device editor already
// documents: components.Tabs keeps the chosen tab as an Element in its paint
// callback, and MyGo paints a closing dialog without rebuilding it, so the
// stale Element panics with "element from pass N used in pass M". Every way out
// of the DERP editor has to survive the closing animation.
func TestDerpEditorClosesWithoutPanic(t *testing.T) {
	cases := []struct {
		name string
		open func(*ViewModel)
		act  func(*testing.T, *ui.Tester)
	}{
		{"cancel on the JSON tab", func(vm *ViewModel) {
			vm.OpenDerp()
			vm.openDerpEditor(&DerpEditorDraft{Name: "a", Kind: "json"})
		}, func(t *testing.T, tt *ui.Tester) { click(t, tt, "取消") }},
		{"cancel on the URL tab", func(vm *ViewModel) {
			vm.OpenDerp()
			vm.openDerpEditor(&DerpEditorDraft{Name: "a", Kind: "url", Content: "https://relay.example.com/map.json"})
		}, func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "JSON")
			click(t, tt, "链接")
			click(t, tt, "取消")
		}},
		{"save an inline map", func(vm *ViewModel) {
			vm.OpenDerp()
			vm.openDerpEditor(&DerpEditorDraft{Name: "公司自建", Kind: "json", Content: derpTestMap})
		}, func(t *testing.T, tt *ui.Tester) { click(t, tt, "保存") }},
		{"delete confirmation", func(vm *ViewModel) {
			vm.OpenDerp()
			preset := core.RelayPreset{ID: uuid.New(), Name: "公司自建", Source: "https://relay.example.com/map.json"}
			vm.DerpConfirmDelete = &preset
		}, func(t *testing.T, tt *ui.Tester) { click(t, tt, "取消") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := newTestViewModel(t)
			tc.open(vm)
			tt := ui.NewTester(vm.ContentView, 1000, 800)
			tt.Frame()
			tc.act(t, tt)
			for i := 0; i < 12; i++ {
				tt.Advance(20 * time.Millisecond)
				tt.Frame()
			}
		})
	}
}

// click drives one button and lets the frame it caused be built.
func click(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.Click(name); err != nil {
		t.Fatalf("Click(%q) error: %v", name, err)
	}
	tt.Advance(20 * time.Millisecond)
	tt.Frame()
}

// TestOfficialRowCountsOnlyTailcatDevices pins that a direct-ADB device, which
// has no DERP map at all, is not counted as a user of the official one.
func TestOfficialRowCountsOnlyTailcatDevices(t *testing.T) {
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{
				{ID: uuid.New(), Name: "lynk08", RemotePort: 5555, RelaySource: "https://relay.example.com/map.json"},
				{ID: uuid.New(), Name: "本地模拟器", DirectADB: true, DirectTarget: "127.0.0.1:5555"},
			},
			Tools: core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDerp()
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("没有设备在用") {
		t.Fatalf("the official map does not say it is unused; texts = %v", tt.Texts())
	}

	// A tailcat device on the official map is counted; the direct one never is.
	if _, err := m.SaveDevice(core.SaveDeviceRequest{Name: "官方设备", Address: testAddress, RemotePort: 5555}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	tt.Frame()
	if !tt.HasText("1 台设备") {
		t.Fatalf("the official map does not count its one tailcat device; texts = %v", tt.Texts())
	}
}

// TestOfficialRowNamesTheTailcatDefaultMap pins the answer to "which map is the
// default": the user cannot judge a row that only says 内置公共地图.
func TestOfficialRowNamesTheTailcatDefaultMap(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDerp()
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText(core.OfficialRelayURL) {
		t.Fatalf("the official row does not name its map; texts = %v", tt.Texts())
	}
}
