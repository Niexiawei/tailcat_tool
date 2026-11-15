package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

const testAddress = "tc" + "AbC123_-xyz789" + "0123456789abcdef"

type stubRunner struct{}

func (stubRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (stubRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg == "get-state" {
			return core.Result{Output: "device\n"}, nil
		}
	}
	return core.Result{}, nil
}

func newTestManager(t *testing.T) (*core.Manager, *core.MemoryCredentialStore) {
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
	return m, creds
}

func newTestViewModel(t *testing.T) *ViewModel {
	t.Helper()
	m, _ := newTestManager(t)
	return NewViewModel(m, func(string) (string, bool) { return "", false })
}

// sidebarTester renders the list and its footer together: they live in two
// separate shell slots now, so one tester must cover both.
func sidebarTester(vm *ViewModel, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			vm.SidebarView(c)
			vm.SidebarFooter(c)
		})
	}, width, height)
}

func TestSidebarGroups(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	for _, want := range []string{"我的设备", "E245", "临时设备", "新建临时连接"} {
		if !tt.HasText(want) {
			t.Fatalf("sidebar lacks %q; texts = %v", want, tt.Texts())
		}
	}

	if _, err := vm.Manager.SaveTemporaryDevice(core.SaveTemporaryDeviceRequest{Address: testAddress, RemotePort: 5555}); err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}
	tt = sidebarTester(vm, 280, 720)
	if !tt.HasText("临时设备 (1)") {
		t.Fatalf("temporary device row missing; texts = %v", tt.Texts())
	}
}

func TestToolHealthRows(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	if !tt.HasText("可用") {
		t.Fatalf("the configured adb is not shown as available; texts = %v", tt.Texts())
	}
	if !tt.HasText("安装") {
		t.Fatalf("a missing scrcpy has no install button; texts = %v", tt.Texts())
	}
}

func TestEmptyDetailState(t *testing.T) {
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:   creds,
		Identities:    core.NewIdentityStore(t.TempDir()),
		Runner:        stubRunner{},
		Locate:        func(string) (string, bool) { return "", false },
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	tt := ui.NewTester(vm.ContentView, 800, 600)
	if !tt.HasText("连接你的第一台设备") {
		t.Fatalf("empty state missing; texts = %v", tt.Texts())
	}
}

func TestDeviceRowShowsState(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	tt := ui.NewTester(vm.SidebarView, 280, 720)
	joined := strings.Join(tt.Texts(), " | ")
	if !strings.Contains(joined, "ADB 已连接") {
		t.Fatalf("the sidebar does not show the online state: %s", joined)
	}
}

func TestSidebarRowsKeepFixedHeight(t *testing.T) {
	vm := newTestViewModel(t)
	for _, name := range []string{"AAA", "BBB", "CCC"} {
		if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: name, Address: testAddress, RemotePort: 5555}); err != nil {
			t.Fatalf("SaveDevice() error: %v", err)
		}
	}
	tt := sidebarTester(vm, 280, 720)
	first, ok := tt.Find("AAA")
	if !ok {
		t.Fatalf("AAAA row missing; texts = %v", tt.Texts())
	}
	second, ok := tt.Find("BBB")
	if !ok {
		t.Fatalf("BBB row missing; texts = %v", tt.Texts())
	}
	// A stretched row would push the next label hundreds of pixels down.
	if pitch := second.Y - first.Y; pitch > 70 {
		t.Fatalf("device rows are %0.0f apart, want a fixed row height", pitch)
	}
}

func TestSidebarAccordionCollapses(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	if !tt.HasText("E245") {
		t.Fatalf("device row missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("我的设备 (1)"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	tt = sidebarTester(vm, 280, 720)
	if tt.HasText("E245") {
		t.Fatalf("the section did not collapse; texts = %v", tt.Texts())
	}
	if vm.SavedOpen {
		t.Fatal("the accordion did not store its collapsed state")
	}
}

func TestEditorRadioSelectsDirectMode(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openEditor(&EditorRequest{})
	render := func() *ui.Tester { return ui.NewTester(vm.ContentView, 900, 700) }
	tt := render()
	if !tt.HasText("Tailcat 地址") {
		t.Fatalf("the Tailcat form is missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("本地 ADB 直连"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.EditorDraft.Mode != "adb" {
		t.Fatalf("mode = %q, want adb", vm.EditorDraft.Mode)
	}
	tt = render()
	if tt.HasText("Tailcat 地址") {
		t.Fatalf("the Tailcat form is still shown; texts = %v", tt.Texts())
	}
	if !tt.HasText("ADB 地址（IP:端口 或 USB 序列号）") {
		t.Fatalf("the direct address field is missing; texts = %v", tt.Texts())
	}
}

func TestDetailShowsDirectModeWithoutTailcatFields(t *testing.T) {
	creds := core.NewMemoryCredentialStore()
	profile := core.Profile{ID: uuid.New(), Name: "本地模拟器", DirectADB: true, DirectTarget: "127.0.0.1:5554"}
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{Profiles: []core.Profile{profile}, Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:   creds,
		Identities:    core.NewIdentityStore(t.TempDir()),
		Runner:        stubRunner{},
		Locate:        func(string) (string, bool) { return "", false },
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	tt := ui.NewTester(vm.ContentView, 900, 900)
	for _, want := range []string{"本地 ADB 直连", "127.0.0.1:5554", "ADB 地址", "复制 ADB 地址"} {
		if !tt.HasText(want) {
			t.Fatalf("direct detail lacks %q; texts = %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"远端端口", "DERP 中继", "复制 ADB 入口"} {
		if tt.HasText(unwanted) {
			t.Fatalf("direct detail still shows %q; texts = %v", unwanted, tt.Texts())
		}
	}
}
