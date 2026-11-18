package app

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

func TestDeviceEditorValidation(t *testing.T) {
	base := EditorInput{Name: "E245", Address: testAddress, RemotePort: "5555"}
	cases := []struct {
		name    string
		mutate  func(*EditorInput)
		wantErr string
	}{
		{"valid", func(*EditorInput) {}, ""},
		{"empty name", func(in *EditorInput) { in.Name = "  " }, "设备名称"},
		{"bad address", func(in *EditorInput) { in.Address = "nope" }, "Tailcat 连接地址"},
		{"local port not a number", func(in *EditorInput) { in.LocalPort = "abc" }, "端口需要填写整数"},
		{"local port out of range", func(in *EditorInput) { in.LocalPort = "70000" }, "1–65535"},
		{"remote port missing", func(in *EditorInput) { in.RemotePort = "" }, "端口需要填写整数"},
		{"custom relay without source", func(in *EditorInput) { in.RelayTab = 1 }, "DERP JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			_, err := in.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want success", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestEditorKeepsAutoPortEmpty(t *testing.T) {
	values, err := (EditorInput{Name: "E245", Address: testAddress, RemotePort: "5555"}).Validate()
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if values.PreferredPort != nil {
		t.Fatalf("PreferredPort = %v, want nil for an empty field", *values.PreferredPort)
	}
}

func TestTransferRemotePathValidation(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"/sdcard/Download/a.txt", false},
		{"/", false},
		{"sdcard/a.txt", true},
		{"", true},
		{"/sdcard/a\nb", true},
	}
	for _, tc := range cases {
		err := ValidateRemotePath(tc.path)
		if tc.wantErr != (err != nil) {
			t.Fatalf("ValidateRemotePath(%q) = %v", tc.path, err)
		}
	}
}

func TestDetailRows(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)

	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	for _, want := range []string{
		"本地入口", "远端端口", "DERP 中继", "链路", "设备型号", "设备代号",
		"Android", "设备序列号", "Shell 权限", "默认节点", "未探测",
		"探测链路", "刷新信息", "设备操作",
	} {
		if !tt.HasText(want) {
			t.Fatalf("detail lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestLogPageShowsFinishedTasks(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	// Tasks reach the tab through history now, so the fake answers with the
	// row the store would have written.
	reader.add(core.TimelineEntry{
		At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogTask,
		DeviceID: profile.ID, DeviceName: "E245", TaskID: uuid.New(),
		Text: "上传 a · 已完成",
	})
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	if !tt.HasText("通道日志与任务") {
		t.Fatalf("the log tab is missing: %v", tt.Texts())
	}
	waitForText(t, tt, "上传 a · 已完成")
}

func TestReachabilityTableRows(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openReachability()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if err := tt.Click("开始测试"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	vm.Manager.Reachability().Wait()
	tt.Frame()
	joined := strings.Join(tt.Texts(), " | ")
	if !strings.Contains(joined, "全部设备连通测试") {
		t.Fatalf("reachability dialog missing: %s", joined)
	}
	if !strings.Contains(joined, "E245") {
		t.Fatalf("reachability table lacks the device: %s", joined)
	}
}

func TestReachabilityWaitsForTheUserToStart(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openReachability()
	if vm.Manager.Reachability().Running() {
		t.Fatal("opening the dialog started probing on its own")
	}
	if rows := vm.Manager.Reachability().Results(); len(rows) != 0 {
		t.Fatalf("opening the dialog built %d rows before the user asked", len(rows))
	}
}

func TestReachabilityStartsFromTheButton(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openReachability()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("还没有测试过") {
		t.Fatalf("the dialog does not explain the empty table: %v", tt.Texts())
	}
	if err := tt.Click("开始测试"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	// The fake prober answers instantly, so wait for the batch instead of
	// sampling Running() the same frame.
	vm.Manager.Reachability().Wait()
	rows := vm.Manager.Reachability().Results()
	if len(rows) == 0 {
		t.Fatal("「开始测试」did not start the batch")
	}
	for _, row := range rows {
		if row.State == core.ReachQueued || row.State == core.ReachTesting {
			t.Fatalf("row %q never finished", row.State.Label())
		}
	}
}

func TestReachabilityCloseButtonClosesTheDialog(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openReachability()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.ReachabilityOpen {
		t.Fatal("the corner close button did not close the dialog")
	}
}

// blockingProber holds every probe until the test releases it, so a batch can
// be closed while it is genuinely still running.
type blockingProber struct{ release chan struct{} }

func (p blockingProber) Ping(ctx context.Context, _ uuid.UUID, _ string, _ int, _ core.RelayConfig) (core.PingOutcome, error) {
	select {
	case <-p.release:
		return core.PingOutcome{Latency: time.Millisecond, Route: "DERP"}, nil
	case <-ctx.Done():
		return core.PingOutcome{}, core.ErrCancelled
	}
}

func TestReachabilityCloseCancelsTheRunningBatch(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
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
		Prober:          blockingProber{release: release},
	})
	for _, profile := range m.Profiles() {
		if err := creds.Write(testAddress, profile.ID); err != nil {
			t.Fatal(err)
		}
	}
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.openReachability()
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if err := tt.Click("开始测试"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !m.Reachability().Running() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !m.Reachability().Running() {
		t.Fatal("the batch never started, so closing it proves nothing")
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.ReachabilityOpen {
		t.Fatal("closing the dialog mid-test did not close it")
	}
	m.Reachability().Wait()
	for _, row := range m.Reachability().Results() {
		if row.State == core.ReachQueued || row.State == core.ReachTesting {
			t.Fatalf("closing the dialog left a row in %q", row.State.Label())
		}
	}
}

func TestReachabilityTruncatedDetailShowsATooltip(t *testing.T) {
	// A device without credentials fails with the long message the table can
	// only show cut off.
	creds := core.NewMemoryCredentialStore()
	m := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{{ID: uuid.New(), Name: "本地模拟器api30", RemotePort: 5555}},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:     creds,
		Identities:      core.NewIdentityStore(t.TempDir()),
		Runner:          stubRunner{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: 3600 * 1e9,
	})
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.openReachability()
	tt := ui.NewTester(vm.ContentView, 1100, 700)
	if err := tt.Click("开始测试"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	m.Reachability().Wait()
	tt.Frame()
	const full = "没有有效连接凭据，请编辑设备后重试"
	if _, ok := tt.AccessByRole(ui.RoleTooltip, full); ok {
		t.Fatal("the tooltip is showing before any hover")
	}
	rect, ok := tt.Find(full)
	if !ok {
		t.Fatalf("no detail cell; texts = %v", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	// MyGo owns the hover delay; one second of clock is enough for it.
	tt.Advance(1500 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleTooltip, full); !ok {
		t.Fatalf("hovering the truncated detail showed no tooltip; access = %v", tt.AccessAll())
	}
}

func TestReachabilityStatusUsesGreenCheckAndRedCross(t *testing.T) {
	th := &ui.Theme{Success: ui.Hex("#00ff00"), Danger: ui.Hex("#ff0000"), TextMuted: ui.Hex("#888888"), Accent: ui.Hex("#0000ff")}
	check, green := reachStatus(th, core.ReachReachable)
	if check == nil || green != th.Success {
		t.Fatalf("reachable row uses color %v, want the success color", green)
	}
	cross, red := reachStatus(th, core.ReachUnreachable)
	if cross == nil || red != th.Danger {
		t.Fatalf("unreachable row uses color %v, want the danger color", red)
	}
	if _, failed := reachStatus(th, core.ReachFailed); failed != th.Danger {
		t.Fatalf("failed row uses color %v, want the danger color", failed)
	}
	if iconPointer(check) == iconPointer(cross) {
		t.Fatal("success and failure share one icon")
	}
	if _, wait := reachStatus(th, core.ReachQueued); wait != th.TextMuted {
		t.Fatalf("queued row uses color %v, want the muted color", wait)
	}
}

func TestDemoModeUsesSimulatedDevices(t *testing.T) {
	env := NewDemoEnvironment()
	if !env.Demo {
		t.Fatal("the demo environment is not marked as a demo")
	}
	if app := New(env); app.vm.Demo != env.Demo {
		t.Fatal("the view model did not inherit the demo flag")
	}
	profiles := env.Manager.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("demo has %d devices, want 2", len(profiles))
	}
	if err := env.Manager.Connect(context.Background(), profiles[0].ID); err != nil {
		t.Fatalf("demo Connect() error: %v", err)
	}
	session := env.Manager.Session(profiles[0].ID)
	if session.State != core.StateOnline || session.Port != 5556 {
		t.Fatalf("demo session = %+v", session)
	}
	env.Manager.Shutdown(context.Background())
}

func TestAttentionDialogAppears(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	// Simulate an external "adb disconnect": the device list no longer has
	// our entry, which the monitor turns into a needs-action state.
	vm.Manager.ForceAttentionForTest(profile.ID, core.AttentionSerialDetached, "本地 ADB 入口已被外部移除")
	tt := ui.NewTester(vm.ContentView, 900, 700)
	joined := strings.Join(tt.Texts(), " | ")
	for _, want := range []string{"设备需要处理", "重新连接", "改用其他本地端口", "稍后处理", "E245"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("attention dialog lacks %q: %s", want, joined)
		}
	}
	vm.Manager.AckAttention(profile.ID)
	vm.Manager.Disconnect(context.Background(), profile.ID)
}

func TestEditorDirectMode(t *testing.T) {
	base := EditorInput{Name: "USB", Mode: "adb", DirectTarget: "emulator-5554"}
	values, err := base.Validate()
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if !values.DirectADB || values.DirectTarget != "emulator-5554" {
		t.Fatalf("values = %+v", values)
	}
	bad := base
	bad.DirectTarget = ""
	if _, err := bad.Validate(); err == nil {
		t.Fatal("an empty direct target was accepted")
	}
	tailcat := base
	tailcat.DirectTarget = testAddress
	if _, err := tailcat.Validate(); err == nil {
		t.Fatal("a Tailcat address was accepted as a direct target")
	}
}

func TestEditorDefaultsToTailcatMode(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openEditor(&EditorRequest{})
	if vm.EditorDraft.Mode != "tailcat" {
		t.Fatalf("default mode = %q, want tailcat", vm.EditorDraft.Mode)
	}
	tt := ui.NewTester(vm.ContentView, 900, 700)
	joined := strings.Join(tt.Texts(), " | ")
	if !strings.Contains(joined, "通过 Tailcat 转发") || !strings.Contains(joined, "本地 ADB 直连") {
		t.Fatalf("the connection mode radio is missing: %s", joined)
	}
}

func TestEditorRelayTabSwitchesToCustom(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openEditor(&EditorRequest{})
	tt := ui.NewTester(vm.ContentView, 900, 1000)
	if tt.HasText("DERP JSON 链接或内容") {
		t.Fatalf("the custom relay field shows before it is chosen; texts = %v", tt.Texts())
	}
	if err := tt.Click("自定义"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.EditorDraft.RelayTab != 1 || !tt.HasText("DERP JSON 链接或内容") {
		t.Fatalf("the segmented switch did not open the custom relay field; tab = %d", vm.EditorDraft.RelayTab)
	}
}

func TestClosingTheEditorDoesNotPanic(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openEditor(&EditorRequest{})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	// The dialog animates out through MyGo's overlay, which keeps painting the
	// layer it was handed: any paint callback inside it must not hold on to an
	// element from an earlier pass.
	for i := 0; i < 10; i++ {
		tt.Advance(20 * time.Millisecond)
		tt.Frame()
	}
	if vm.PendingEditor != nil {
		t.Fatal("the editor did not close")
	}
}

// iconPointer identifies an icon accessor, so a test can tell two icons apart.
func iconPointer(icon func() *ui.SVG) uintptr {
	if icon == nil {
		return 0
	}
	return reflect.ValueOf(icon).Pointer()
}

func TestTruncatedDeviceNamesShowATooltip(t *testing.T) {
	vm := newTestViewModel(t)
	// The sidebar is 220 wide and the card's text column is narrower still, so
	// a long name is cut off with an ellipsis; hovering must give the full one.
	long := "本地模拟器api30-实验室里那台专门跑回归的设备"
	id := vm.Manager.Profiles()[0].ID
	if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
		ID: &id, Name: long, Address: testAddress, RemotePort: 5555,
	}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	for _, tc := range []struct {
		name string
		view func(c *ui.Context)
		w, h int
	}{
		{"sidebar", vm.SidebarView, 220, 600},
		{"index card", vm.ContentView, 700, 400},
	} {
		tt := ui.NewTester(tc.view, tc.w, tc.h)
		rect, ok := tt.Find(long)
		if !ok {
			t.Fatalf("%s: no row for the long name; texts = %v", tc.name, tt.Texts())
		}
		tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		tt.Advance(1500 * time.Millisecond)
		tt.Frame()
		if _, ok := tt.AccessByRole(ui.RoleTooltip, long); !ok {
			t.Fatalf("%s: hovering the clipped name showed no tooltip", tc.name)
		}
	}
}

func TestRelaySourceOptionsMergePresetsAndHistory(t *testing.T) {
	presets := []core.RelayPreset{
		{Name: "公司自建", Source: "https://relay.example.com/map.json"},
		{Name: "内网", Source: `{"Regions":{}}`},
	}
	history := []core.RelayHistoryEntry{
		// The same source as a preset: listed once, under the user's name.
		{Name: "relay.example.com", Source: "https://relay.example.com/map.json"},
		{Name: "evcc.coauto.cc", Source: "https://evcc.coauto.cc/map.json"},
	}
	options := relaySourceOptions(presets, history)
	want := []struct{ label, value string }{
		{"预设 · 公司自建", "https://relay.example.com/map.json"},
		{"预设 · 内网", `{"Regions":{}}`},
		{"历史 · evcc.coauto.cc", "https://evcc.coauto.cc/map.json"},
	}
	if len(options) != len(want) {
		t.Fatalf("options = %+v, want %d", options, len(want))
	}
	for i, w := range want {
		if options[i].Label != w.label || options[i].Value != w.value {
			t.Fatalf("options[%d] = %+v, want %+v", i, options[i], w)
		}
	}
}

func TestEditorPicksAPresetFromTheMergedMenu(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	vm.openEditor(&EditorRequest{})
	tt := ui.NewTester(vm.ContentView, 900, 1000)
	if err := tt.Click("自定义"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if !tt.HasText("从预设或历史选择") {
		t.Fatalf("the merged picker is missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("DERP 节点来源"); err != nil {
		t.Fatalf("opening the menu: %v", err)
	}
	if err := tt.ChooseMenuItem("预设 · 公司自建"); err != nil {
		t.Fatalf("ChooseMenuItem() error: %v", err)
	}
	if vm.EditorDraft.RelaySource != preset.Source {
		t.Fatalf("RelaySource = %q, want the preset's %q", vm.EditorDraft.RelaySource, preset.Source)
	}
	if vm.EditorDraft.RelayPick != preset.Source {
		t.Fatalf("RelayPick = %q", vm.EditorDraft.RelayPick)
	}
}

func TestEditorPrefillsThePresetPickerWhenEditingADevice(t *testing.T) {
	vm, preset := newDerpViewModel(t)
	profile := vm.Manager.Profiles()[0]
	profile.RelaySource = preset.Source
	vm.openEditor(&EditorRequest{Profile: &profile})
	if vm.EditorDraft.RelayPick != preset.Source {
		t.Fatalf("RelayPick = %q, want the device's preset", vm.EditorDraft.RelayPick)
	}
}
