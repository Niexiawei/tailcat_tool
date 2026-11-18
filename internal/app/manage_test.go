package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
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
		t.Fatalf("title = %v, want 我的设备 › 缓存管理", title.Texts())
	}
}

// TestDerpDetailBreadcrumbHasTheListNode pins the trail: a detail page sits
// under the list, so the title bar must offer the way back to it.
func TestDerpDetailBreadcrumbHasTheListNode(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	vm.OpenDerpDetail(preset.ID)
	title := ui.NewTester(vm.ContentTitle, 700, 40)
	for _, want := range []string{"我的设备", "DERP 节点", preset.Name} {
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
