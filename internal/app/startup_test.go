package app

import (
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// startupProblems is a page with three of the four failures at once, which is
// what a half-written configuration and a full disk look like together.
func startupProblems() []core.Problem {
	return []core.Problem{
		{Kind: core.ProblemConfig, Title: "配置读取失败，已使用默认设置", Detail: "unexpected end of JSON input", Path: "/config", Continue: true},
		{Kind: core.ProblemHistory, Title: "历史库无法打开，本次运行不会保存历史", Detail: "disk full", Path: "/config", Continue: true},
		{Kind: core.ProblemADB, Title: "未找到 adb，连接设备前需要先安装", Continue: true},
	}
}

func startupTester(vm *ViewModel) *ui.Tester {
	return ui.NewTester(vm.ContentView, 900, 700)
}

// The page exists because four failures used to be four restarts: each one was
// thrown away after the first, so the user fixed what it said and found the
// next problem only next time.
func TestStartupPageListsEveryProblem(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	vm.OpenDir = func(string) error { return nil }
	vm.Quit = func() {}

	tt := startupTester(vm)
	for _, problem := range startupProblems() {
		if !tt.HasText(problem.Title) {
			t.Fatalf("the startup page lacks %q; texts = %v", problem.Title, tt.Texts())
		}
	}
	if !tt.HasText("/config") {
		t.Fatalf("the page does not say where the file is; texts = %v", tt.Texts())
	}
	if !tt.HasText("disk full") {
		t.Fatalf("the page drops the failure's own words; texts = %v", tt.Texts())
	}
	for _, want := range []string{"用默认设置继续", "打开配置目录", "退出"} {
		if !tt.HasText(want) {
			t.Fatalf("the startup page lacks the %q action; texts = %v", want, tt.Texts())
		}
	}
	// The adb problem is fixed in the tool settings, not in a directory.
	if !tt.HasText("打开工具设置") {
		t.Fatalf("a missing adb offers no way to the tool settings; texts = %v", tt.Texts())
	}
}

// The page replaces the routed content: a device list under three problems is
// a page that looks fine and is not.
func TestStartupPageHidesTheRoutedContent(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	vm.OpenDir = func(string) error { return nil }
	vm.Quit = func() {}

	tt := startupTester(vm)
	if tt.HasText("测试全部设备") || tt.HasText("添加设备") {
		t.Fatalf("the device index is still drawn under the startup page; texts = %v", tt.Texts())
	}
}

func TestStartupPageContinuesToTheDeviceList(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	vm.OpenDir = func(string) error { return nil }
	vm.Quit = func() {}
	vm.OpenDerp() // start somewhere else to prove the button routes

	startupTester(vm).Click("用默认设置继续")

	if vm.StartupOpen {
		t.Fatal("���续 left the startup page open")
	}
	if path := vm.Router.Path(); path != DeviceIndexRoute {
		t.Fatalf("router path = %q, want the device list", path)
	}
	if !startupTester(vm).HasText("添加设备") {
		t.Fatal("the device list did not replace the startup page")
	}
}

func TestStartupPageOpensTheConfigurationDirectory(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	var opened string
	vm.OpenDir = func(dir string) error { opened = dir; return nil }
	vm.Quit = func() {}

	startupTester(vm).Click("打开配置目录")

	if opened != "/config" {
		t.Fatalf("opened %q, want the configuration directory the problem names", opened)
	}
	if !vm.StartupOpen {
		t.Fatal("opening the directory closed the page; the user has not fixed anything yet")
	}
}

// A failure to open the directory is reported on the page, not swallowed: the
// user clicked a button and nothing happened.
func TestStartupPageShowsWhyTheDirectoryWouldNotOpen(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	vm.OpenDir = func(string) error { return core.Error("无法打开目录：权限不足") }
	vm.Quit = func() {}

	tt := startupTester(vm)
	tt.Click("打开配置目录")

	if !startupTester(vm).HasText("无法打开目录：权限不足") {
		t.Fatal("the failure to open the directory is not on the page")
	}
}

func TestStartupPageQuits(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = startupProblems()
	vm.OpenDir = func(string) error { return nil }
	quit := false
	vm.Quit = func() { quit = true }

	startupTester(vm).Click("退出")

	if !quit {
		t.Fatal("退出 did not quit")
	}
}

// A missing adb is fixed in the tool settings, so that button goes there
// instead of offering a second page the user has to find by itself.
func TestStartupPageOpensTheToolSettingsForMissingADB(t *testing.T) {
	vm := newTestViewModel(t)
	vm.StartupOpen = true
	vm.StartupProblems = []core.Problem{
		{Kind: core.ProblemADB, Title: "未找到 adb，连接设备前需要先安装", Continue: true},
	}
	vm.OpenDir = func(string) error { return nil }
	vm.Quit = func() {}

	startupTester(vm).Click("打开工具设置")

	if vm.StartupOpen {
		t.Fatal("the startup page stayed on top of the tool settings")
	}
	if path := vm.Router.Path(); path != SettingsRoute {
		t.Fatalf("router path = %q, want the tool settings", path)
	}
}

// A healthy start draws no page at all.
func TestStartupPageStaysShutWithoutProblems(t *testing.T) {
	vm := newTestViewModel(t)
	if vm.StartupOpen {
		t.Fatal("a healthy start opened the startup page")
	}
	if startupTester(vm).HasText("用默认设置继续") {
		t.Fatal("the startup page is drawn without problems")
	}
}
