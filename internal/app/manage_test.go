package app

import (
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

func TestSidebarShowsManageEntriesBelowDevices(t *testing.T) {
	vm := newTestViewModel(t) // 已有一台 E245
	tt := sidebarTester(vm, 280, 720)
	device, ok := tt.Find("E245")
	if !ok {
		t.Fatalf("device row missing; texts = %v", tt.Texts())
	}
	entry, ok := tt.Find("缓存管理")
	if !ok {
		t.Fatalf("cache entry missing; texts = %v", tt.Texts())
	}
	if entry.Y <= device.Y {
		t.Fatalf("缓存管理 is above the device list (%0.0f <= %0.0f)", entry.Y, device.Y)
	}
	if !tt.HasText("DERP 节点") {
		t.Fatalf("DERP entry missing; texts = %v", tt.Texts())
	}
}

func TestManageEntryOpensCachePage(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	if err := tt.Click("缓存管理"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != CacheRoute {
		t.Fatalf("path = %q, want %q", got, CacheRoute)
	}
	page := ui.NewTester(vm.ContentView, 900, 700)
	if !page.HasText("应用信息缓存") {
		t.Fatalf("cache page missing; texts = %v", page.Texts())
	}
	title := ui.NewTester(vm.ContentTitle, 400, 40)
	if !title.HasText("缓存管理") {
		t.Fatalf("title = %v, want 首页 › 缓存管理", title.Texts())
	}
}

// TestDerpDetailBreadcrumbHasTheListNode pins the trail: a detail page sits
// under the list, so the title bar must offer the way back to it.
func TestDerpDetailBreadcrumbHasTheListNode(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	vm.OpenDerpDetail(preset.ID)
	title := ui.NewTester(vm.ContentTitle, 700, 40)
	for _, want := range []string{"首页", "DERP 节点", preset.Name} {
		if !title.HasText(want) {
			t.Fatalf("the title bar lacks %q; texts = %v", want, title.Texts())
		}
	}
	if err := title.Click("DERP 节点"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != DerpRoute {
		t.Fatalf("path = %q after the breadcrumb, want %q", got, DerpRoute)
	}
}

// TestManagePageLeavesNoDeviceSelected pins the sidebar's claim: a device row
// is tinted only while that device's page is open. A management page next to a
// highlighted device reads as "these cached apps belong to E245", which they do
// not — the page has its own device picker.
func TestManagePageLeavesNoDeviceSelected(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.OpenDevice(profile.ID)
	content := ui.NewTester(vm.ContentView, 900, 700)
	content.Frame()
	if vm.Selection != profile.ID {
		t.Fatalf("the device page did not select its device: %v", vm.Selection)
	}

	pages := []struct {
		name string
		open func()
	}{
		{"cache", vm.OpenCache},
		{"derp", vm.OpenDerp},
		{"logs", vm.OpenLogs},
		{"tools", vm.OpenTools},
	}
	for _, page := range pages {
		page.open()
		content.Frame()
		if vm.Selection != uuid.Nil {
			t.Fatalf("%s page kept %v selected; route = %q", page.name, vm.Selection, vm.Router.Path())
		}
	}

	// Going back to a device page tints its row again: the route is still the
	// only thing that decides, not a leftover flag.
	vm.OpenDevice(profile.ID)
	content.Frame()
	if vm.Selection != profile.ID {
		t.Fatalf("the device page lost its selection: %v", vm.Selection)
	}
}

// TestCachePageOpensOnTheDeviceTheUserCameFrom keeps the handoff the sidebar
// tint used to provide implicitly: opening 缓存管理 from a device page shows
// that device's cache the first time.
func TestCachePageOpensOnTheDeviceTheUserCameFrom(t *testing.T) {
	vm := newTestViewModel(t)
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		Name: "客厅", Address: testAddress, RemotePort: 5555,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	profiles := vm.Manager.Profiles()
	last := profiles[len(profiles)-1]
	vm.OpenDevice(last.ID)
	vm.OpenCache()
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	tt.Frame()
	if vm.CacheDevice != last.ID {
		t.Fatalf("cache page shows %v, want the device the user came from (%v)", vm.CacheDevice, last.ID)
	}
}
