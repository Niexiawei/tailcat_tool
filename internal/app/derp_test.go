package app

import (
	"os"
	"testing"

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
