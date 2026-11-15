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

func TestSidebarGroups(t *testing.T) {
	vm := newTestViewModel(t)
	tt := ui.NewTester(vm.SidebarView, 280, 720)
	for _, want := range []string{"我的设备", "E245", "临时设备", "新建临时连接"} {
		if !tt.HasText(want) {
			t.Fatalf("sidebar lacks %q; texts = %v", want, tt.Texts())
		}
	}

	if _, err := vm.Manager.SaveTemporaryDevice(core.SaveTemporaryDeviceRequest{Address: testAddress, RemotePort: 5555}); err != nil {
		t.Fatalf("SaveTemporaryDevice() error: %v", err)
	}
	tt = ui.NewTester(vm.SidebarView, 280, 720)
	if !tt.HasText("临时设备 1") {
		t.Fatalf("temporary device row missing; texts = %v", tt.Texts())
	}
}

func TestToolHealthRows(t *testing.T) {
	vm := newTestViewModel(t)
	tt := ui.NewTester(vm.SidebarView, 280, 720)
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
