package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/shells/sidebar"
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

func TestToolbarOnlyOnTheIndexPage(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]

	index := ui.NewTester(vm.ContentView, 900, 700)
	if !index.HasText("测试全部设备") || !index.HasText("添加设备") {
		t.Fatalf("the index page lost the window actions; texts = %v", index.Texts())
	}

	vm.OpenDevice(profile.ID)
	device := ui.NewTester(vm.ContentView, 900, 700)
	for _, unwanted := range []string{"测试全部设备", "临时连接", "添加设备"} {
		if device.HasText(unwanted) {
			t.Fatalf("the device page still shows %q; texts = %v", unwanted, device.Texts())
		}
	}
}

func TestDirectDeviceHidesTheProbe(t *testing.T) {
	profile := core.Profile{ID: uuid.New(), Name: "本地", DirectADB: true, DirectTarget: "127.0.0.1:5554"}
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{Profiles: []core.Profile{profile}, Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:   core.NewMemoryCredentialStore(),
		Identities:    core.NewIdentityStore(t.TempDir()),
		Runner:        stubRunner{},
		Locate:        func(string) (string, bool) { return "", false },
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("探测链路") {
		t.Fatalf("a direct device still offers the Tailcat probe; texts = %v", tt.Texts())
	}
	if !tt.HasText("刷新信息") {
		t.Fatalf("the refresh action is gone too; texts = %v", tt.Texts())
	}
}

func TestSidebarGroups(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	// The footer no longer offers the temporary connection: it lives in
	// the toolbar, which only the index page shows.
	for _, want := range []string{"我的设备", "E245", "临时设备"} {
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
	vm.OpenDevice(profile.ID)
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

func TestErrorsBecomeErrorToasts(t *testing.T) {
	vm := newTestViewModel(t)
	vm.FailText("adb 连接超时")

	queued := vm.drainToasts()
	if len(queued) != 1 {
		t.Fatalf("queued %d toasts, want 1", len(queued))
	}
	if queued[0].Type != components.ToastError || queued[0].Description != "adb 连接超时" {
		t.Fatalf("toast = %+v", queued[0])
	}
	if queued[0].Title != "操作失败" {
		t.Fatalf("title = %q", queued[0].Title)
	}
	// Drained: the next frame must not show it again.
	if again := vm.drainToasts(); len(again) != 0 {
		t.Fatalf("a drained toast was queued again: %+v", again)
	}
}

func TestSuccessBecomeSuccessToasts(t *testing.T) {
	vm := newTestViewModel(t)
	vm.Notify("已复制 127.0.0.1:5555")
	queued := vm.drainToasts()
	if len(queued) != 1 || queued[0].Type != components.ToastSuccess {
		t.Fatalf("toasts = %+v", queued)
	}
}

func TestNilErrorQueuesNoToast(t *testing.T) {
	vm := newTestViewModel(t)
	vm.Fail(nil)
	if queued := vm.drainToasts(); len(queued) != 0 {
		t.Fatalf("a nil error produced a toast: %+v", queued)
	}
}

func TestConnectFailureIsReportedAsToast(t *testing.T) {
	// The same call the connect button makes: the error must reach the user
	// as an error toast instead of only landing in the session.
	profile := core.Profile{ID: uuid.New(), Name: "本地", DirectADB: true, DirectTarget: "127.0.0.1:5554"}
	runner := failRunner{}
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{Profiles: []core.Profile{profile}, Tools: core.ToolSettings{ADB: os.Args[0]}},
		Credentials:   core.NewMemoryCredentialStore(),
		Identities:    core.NewIdentityStore(t.TempDir()),
		Runner:        runner,
		Locate:        func(string) (string, bool) { return "", false },
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.Fail(m.Connect(context.Background(), profile.ID))

	queued := vm.drainToasts()
	if len(queued) != 1 || queued[0].Type != components.ToastError {
		t.Fatalf("toasts = %+v", queued)
	}
	if !strings.Contains(queued[0].Description, "offline") {
		t.Fatalf("the toast lost adb's message: %q", queued[0].Description)
	}
}

// failRunner reports every adb call as failing.
type failRunner struct{}

func (failRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("adb: device offline")
}

func (failRunner) Run(context.Context, core.Command, func(string)) (core.Result, error) {
	return core.Result{}, core.Error("adb: device offline")
}

func TestDeviceIndexPageListsDevices(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("E245") {
		t.Fatalf("the index page is missing a device; texts = %v", tt.Texts())
	}
	if err := tt.Click("E245"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got, want := vm.Router.Path(), DeviceRoute(profile.ID); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
}

func TestBackReturnsToPreviousDevice(t *testing.T) {
	vm := newTestViewModel(t)
	first := vm.Manager.Profiles()[0]
	secondID, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: "第二台", Address: testAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.OpenDevice(first.ID)
	vm.OpenDevice(secondID)
	vm.Router.Back()
	if got, want := vm.Router.Path(), DeviceRoute(first.ID); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
	// The page selects the device it shows.
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("E245") {
		t.Fatalf("the restored page shows another device; texts = %v", tt.Texts())
	}
	if vm.Selection != first.ID {
		t.Fatalf("selection = %v, want %v", vm.Selection, first.ID)
	}
}

func TestEditEntryOpensTheEditor(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if err := tt.Click("编辑"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.PendingEditor == nil || vm.PendingEditor.Profile == nil {
		t.Fatal("the edit entry did not open the device form")
	}
	if vm.PendingEditor.Profile.ID != profile.ID {
		t.Fatalf("editor opened on %v", vm.PendingEditor.Profile.ID)
	}
}

func TestMissingDevicePage(t *testing.T) {
	vm := newTestViewModel(t)
	vm.Router.Push("/devices/" + uuid.New().String())
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("设备不存在") {
		t.Fatalf("no page for a device that is gone; texts = %v", tt.Texts())
	}
}

// recordingRunner answers adb through fn and remembers every command line.
type recordingRunner struct {
	fn    func(core.Command) (core.Result, error)
	calls []string
}

func (r *recordingRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (r *recordingRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	r.calls = append(r.calls, strings.Join(c.Args, " "))
	return r.fn(c)
}

// newRunnerViewModel builds a view model whose manager answers adb with fn.
func newRunnerViewModel(t *testing.T, fn func(core.Command) (core.Result, error)) (*ViewModel, func() []string) {
	t.Helper()
	runner := &recordingRunner{fn: fn}
	creds := core.NewMemoryCredentialStore()
	profile := core.Profile{ID: uuid.New(), Name: "E245", RemotePort: 5555}
	if err := creds.Write(testAddress, profile.ID); err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{profile},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          runner,
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	return vm, func() []string { return runner.calls }
}

func TestTaskPanelIsGone(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "adb cat", []string{"cat", "/proc/meminfo"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	vm.Manager.WaitTask(taskID)

	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("设备任务") {
		t.Fatalf("the fixed task panel is still there; texts = %v", tt.Texts())
	}
}

// blockingRunner answers everything except `install` as a healthy device, and
// holds `install` until release is closed or the task is cancelled. Connecting
// and validating the target must keep working, or the task never starts.
type blockingRunner struct{ release chan struct{} }

func (r *blockingRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (r *blockingRunner) Run(ctx context.Context, c core.Command, _ func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg != "install" {
			continue
		}
		select {
		case <-r.release:
			return core.Result{}, nil
		case <-ctx.Done():
			return core.Result{}, core.Error("已取消")
		}
	}
	return core.Result{Output: "device"}, nil
}

// newBlockingViewModel builds a view model whose manager runs commands through
// runner, so a test can hold a task in the running state.
func newBlockingViewModel(t *testing.T, runner core.Runner) *ViewModel {
	t.Helper()
	creds := core.NewMemoryCredentialStore()
	profile := core.Profile{ID: uuid.New(), Name: "E245", RemotePort: 5555}
	if err := creds.Write(testAddress, profile.ID); err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{profile},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          runner,
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	return NewViewModel(m, func(string) (string, bool) { return "", false })
}

func TestContentStaysBelowAFloatingHeader(t *testing.T) {
	for _, bar := range []ui.TitleBar{{Height: 48}, {Height: 64}} {
		vm := newTestViewModel(t)
		vm.OpenDevice(vm.Manager.Profiles()[0].ID)
		var header float32
		tt := ui.NewTester(func(c *ui.Context) {
			header = sidebar.HeaderHeight(c)
			vm.ContentView(c)
		}, 900, 700)
		tt.SetTitleBar(bar)
		tt.Frame()
		// The device name and the action row are the topmost body content;
		// the shell's floating header owns the strip above this height.
		for _, text := range []string{"E245", "编辑"} {
			rect, ok := tt.Find(text)
			if !ok {
				t.Fatalf("titlebar %v: %q is missing; texts = %v", bar, text, tt.Texts())
			}
			if rect.Y < header {
				t.Fatalf("titlebar %v: %q (y=%.0f) is under the floating header (h=%.0f)", bar, text, rect.Y, header)
			}
		}
	}
}

func TestIndexPageStaysBelowAFloatingHeader(t *testing.T) {
	vm := newTestViewModel(t)
	var header float32
	tt := ui.NewTester(func(c *ui.Context) {
		header = sidebar.HeaderHeight(c)
		vm.ContentView(c)
	}, 900, 700)
	rect, ok := tt.Find("测试全部设备")
	if !ok {
		t.Fatalf("the toolbar is missing; texts = %v", tt.Texts())
	}
	if rect.Y < header {
		t.Fatalf("the toolbar (y=%.0f) is under the floating header (h=%.0f)", rect.Y, header)
	}
}

func TestDeviceIDFromPath(t *testing.T) {
	id := uuid.New()
	if got, ok := deviceIDFromPath(DeviceRoute(id)); !ok || got != id {
		t.Fatalf("deviceIDFromPath(%q) = %v, %v", DeviceRoute(id), got, ok)
	}
	for _, path := range []string{DeviceIndexRoute, "", "/devices/nope", "/devices/" + id.String() + "/extra"} {
		if _, ok := deviceIDFromPath(path); ok {
			t.Fatalf("deviceIDFromPath(%q) accepted a non-device path", path)
		}
	}
}

func TestContentTitleShowsTheDevicePath(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	if !tt.HasText("我的设备") || !tt.HasText("E245") {
		t.Fatalf("the title bar does not show the path; texts = %v", tt.Texts())
	}
}

func TestContentTitleBreadcrumbReturnsToTheDeviceIndex(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	// The title bar is a strip of its own, so the click never races the
	// window-drag area the way it did inside ContentView.
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	if err := tt.Click("我的设备"); err != nil {
		t.Fatalf("Click() on the breadcrumb error: %v", err)
	}
	if got := vm.Router.Path(); got != DeviceIndexRoute {
		t.Fatalf("route = %q, want %q", got, DeviceIndexRoute)
	}
}

func TestContentTitleFollowsTheRouter(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	vm.Router.Back()
	tt.Frame()
	if tt.HasText("E245") {
		t.Fatalf("the title bar kept the device after going back; texts = %v", tt.Texts())
	}
	if !tt.HasText("我的设备") {
		t.Fatalf("the title bar lost the page name; texts = %v", tt.Texts())
	}
}

func TestDevicePageHasNoSecondBreadcrumb(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	// The body must not repeat navigation the title bar already shows; the
	// device name is body content, so only the crumb text counts.
	for _, text := range tt.Texts() {
		if text == "我的设备" {
			t.Fatalf("the device page repeats the breadcrumb in the body; texts = %v", tt.Texts())
		}
	}
}

func TestSidebarFooterDropsTheChannelCount(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 420)
	if tt.HasText("条通道") {
		t.Fatalf("the footer still counts channels; texts = %v", tt.Texts())
	}
	if !tt.HasText("scrcpy") {
		t.Fatalf("the footer lost the tool health rows; texts = %v", tt.Texts())
	}
}

func TestIndexToolbarPutsAddDeviceFirst(t *testing.T) {
	vm := newTestViewModel(t)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	add, ok := tt.Find("添加设备")
	if !ok {
		t.Fatalf("the toolbar lost 「添加设备」; texts = %v", tt.Texts())
	}
	temporary, ok := tt.Find("临时连接")
	if !ok {
		t.Fatalf("the toolbar lost 「临时连接」; texts = %v", tt.Texts())
	}
	probe, ok := tt.Find("测试全部设备")
	if !ok {
		t.Fatalf("the toolbar lost 「测试全部设备」; texts = %v", tt.Texts())
	}
	if !(add.X < temporary.X && temporary.X < probe.X) {
		t.Fatalf("toolbar order is add=%.0f temporary=%.0f probe=%.0f, want add first", add.X, temporary.X, probe.X)
	}
	if tt.HasText("工具设置") {
		t.Fatalf("tool settings still sits in the page toolbar; texts = %v", tt.Texts())
	}
}

func TestSidebarFooterOpensToolSettings(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 420)
	if !tt.HasText("工具设置") {
		t.Fatalf("the footer has no tool settings action; texts = %v", tt.Texts())
	}
	if err := tt.Click("工具设置"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if !vm.SettingsOpen {
		t.Fatal("the footer action did not open the settings dialog")
	}
}
