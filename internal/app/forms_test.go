package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestDeviceEditorValidation(t *testing.T) {
	base := EditorInput{Name: "E245", Address: testAddress, RemotePort: "5555", RelaySelection: "default"}
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
		{"custom relay without source", func(in *EditorInput) { in.RelaySelection = "custom" }, "DERP JSON"},
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

	tt := ui.NewTester(vm.ContentView, 900, 700)
	for _, want := range []string{
		"本地入口", "远端端口", "DERP 中继", "链路", "设备型号", "设备代号",
		"Android", "设备序列号", "Shell 权限", "官方默认节点", "未探测",
		"探测链路", "刷新信息", "设备操作",
	} {
		if !tt.HasText(want) {
			t.Fatalf("detail lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestTaskPanelShowsTasks(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "上传 a", []string{"push", "/tmp/a", "/sdcard/a"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	vm.Manager.WaitTask(taskID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("设备任务") || !tt.HasText("上传 a") {
		t.Fatalf("task panel missing: %v", tt.Texts())
	}
}

func TestReachabilityTableRows(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openReachability()
	deadline := time.Now().Add(3 * time.Second)
	for vm.Manager.Reachability().Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	tt := ui.NewTester(vm.ContentView, 900, 700)
	joined := strings.Join(tt.Texts(), " | ")
	if !strings.Contains(joined, "全部设备连通测试") {
		t.Fatalf("reachability dialog missing: %s", joined)
	}
	if !strings.Contains(joined, "E245") {
		t.Fatalf("reachability table lacks the device: %s", joined)
	}
}

func TestDemoModeUsesSimulatedDevices(t *testing.T) {
	env := NewDemoEnvironment()
	if !env.Demo {
		t.Fatal("the demo environment is not marked as a demo")
	}
	vm := New(env)
	vm.vm.Notice = "demo"
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
