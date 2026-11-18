package app

import (
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// TestSidebarMenuOpensToolSettings pins the move: the tool paths are a page of
// their own now, reached from the same list as the other management pages.
func TestSidebarMenuOpensToolSettings(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	if !tt.HasText("工具设置") {
		t.Fatalf("the sidebar has no tool settings entry; texts = %v", tt.Texts())
	}
	if err := tt.Click("工具设置"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != SettingsRoute {
		t.Fatalf("path = %q, want %q", got, SettingsRoute)
	}
}

// TestSidebarFooterDropsTheSettingsAction pins the other half of the move: one
// entry, in one place — the footer keeps the health rows and nothing else.
func TestSidebarFooterDropsTheSettingsAction(t *testing.T) {
	vm := newTestViewModel(t)
	tt := ui.NewTester(vm.SidebarFooter, 280, 200)
	tt.Frame()
	if tt.HasText("工具设置") {
		t.Fatalf("the footer still offers settings; texts = %v", tt.Texts())
	}
	if !tt.HasText("scrcpy") {
		t.Fatalf("the footer lost the tool health rows; texts = %v", tt.Texts())
	}
}

func TestToolSettingsPageShowsBothTools(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenTools()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	for _, want := range []string{"工具设置", "ADB", "scrcpy", "保存"} {
		if !tt.HasText(want) {
			t.Fatalf("the settings page lacks %q; texts = %v", want, tt.Texts())
		}
	}
	// The page names the file each tool resolves to, so "it works" is checkable
	// without running a connection.
	if !tt.HasText(vm.Manager.Tools().ADB) {
		t.Fatalf("the page does not show the ADB path; texts = %v", tt.Texts())
	}
}

// TestToolSettingsPageKeepsWhatTheUserTyped guards the page against reloading
// the saved settings on every frame: a view that did would erase the box the
// user is editing the moment anything else redraws.
func TestToolSettingsPageKeepsWhatTheUserTyped(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenTools()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	vm.SettingsDraft.ADB = "/opt/homebrew/bin/adb"
	tt.Frame()
	if vm.SettingsDraft.ADB != "/opt/homebrew/bin/adb" {
		t.Fatalf("the page reloaded the saved settings over the draft: %q", vm.SettingsDraft.ADB)
	}
	// Leaving and coming back starts from what is saved again.
	vm.OpenDevices()
	tt.Frame()
	vm.OpenTools()
	tt.Frame()
	if got := vm.SettingsDraft.ADB; got != vm.Manager.Tools().ADB {
		t.Fatalf("reopening the page kept the discarded draft: %q", got)
	}
}

func TestToolSettingsPageSavesTheDraft(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenTools()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	vm.SettingsDraft.ADB = "/usr/local/bin/adb"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Manager.Tools().ADB; got != "/usr/local/bin/adb" {
		t.Fatalf("saved ADB = %q, want the draft", got)
	}
	if !tt.HasText("已保存工具设置") {
		t.Fatalf("no toast after saving; texts = %v", tt.Texts())
	}
	if got := vm.Router.Path(); got != SettingsRoute {
		t.Fatalf("saving navigated away: path = %q", got)
	}
}

// TestToolSettingsPageShowsASaveRefusal pins where a refusal is read: on the
// page above the 保存 button, not as a toast the user has to remember while
// looking at the field that caused it.
func TestToolSettingsPageShowsASaveRefusal(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenTools()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	// The wording is core's; the page only has to carry it.
	vm.SettingsError = "请先断开应用管理的连接，再修改工具路径"
	tt.Frame()
	if !tt.HasText("请先断开应用管理的连接") {
		t.Fatalf("the refusal is not on the page; texts = %v", tt.Texts())
	}
}

func TestToolSettingsPageOffersInstallForAMissingTool(t *testing.T) {
	vm := newTestViewModel(t)
	var installed []core.ToolComponent
	vm.Install = func(component core.ToolComponent) { installed = append(installed, component) }
	vm.OpenTools()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	if !tt.HasText("未找到") {
		t.Fatalf("a tool this machine cannot locate is not marked; texts = %v", tt.Texts())
	}
	if err := tt.Click("安装"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if len(installed) == 0 {
		t.Fatal("the install button did not ask for an install")
	}
}

func TestToolSettingsBreadcrumbIsUnderHome(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenTools()
	title := ui.NewTester(vm.ContentTitle, 400, 40)
	for _, want := range []string{"首页", "工具设置"} {
		if !title.HasText(want) {
			t.Fatalf("title = %v, want 首页 › 工具设置", title.Texts())
		}
	}
	if err := title.Click("首页"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != DeviceIndexRoute {
		t.Fatalf("path = %q after the breadcrumb, want %q", got, DeviceIndexRoute)
	}
}
