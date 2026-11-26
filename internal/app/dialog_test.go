package app

import (
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// TestDialogsCloseWithoutPanic frames each dialog through its exit animation.
// MyGo keeps painting a closing layer without rebuilding it, so any paint
// callback inside a dialog that holds an element from an earlier pass panics
// with "element from pass N used in pass M" — which is how the relay switch
// crashed 「添加设备 → 取消」.
func TestDialogsCloseWithoutPanic(t *testing.T) {
	cases := []struct {
		name  string
		open  func(*ViewModel)
		close func(*ViewModel)
	}{
		{"device editor", func(vm *ViewModel) { vm.openEditor(&EditorRequest{}) }, func(vm *ViewModel) { vm.PendingEditor = nil }},
		{"transfer", func(vm *ViewModel) { vm.openTransfer(vm.Selection, true) }, func(vm *ViewModel) { vm.closeTransfer() }},
		{"reachability", func(vm *ViewModel) { vm.ReachabilityOpen = true }, func(vm *ViewModel) { vm.ReachabilityOpen = false }},
		{"attention", func(vm *ViewModel) {
			vm.Manager.ForceAttentionForTest(vm.Manager.Profiles()[0].ID, core.AttentionSerialDetached, "本地 ADB 入口已被外部移除")
		}, func(vm *ViewModel) { vm.Manager.AckAttention(vm.Manager.Profiles()[0].ID) }},
		{"cache confirm", func(vm *ViewModel) {
			vm.CacheConfirmOpen = true
			vm.CacheConfirmPkg = "com.tencent.mm"
		}, func(vm *ViewModel) { vm.closeCacheConfirm() }},
		{"file detail", func(vm *ViewModel) {
			vm.FileDetail = &FileDetail{Device: vm.Selection, Entry: core.RemoteEntry{Name: "log.txt", Path: "/sdcard/log.txt", Size: 2048}}
		}, func(vm *ViewModel) { vm.FileDetail = nil }},
		{"apk save", func(vm *ViewModel) {
			vm.beginAPKSave(vm.Selection, core.AppUsage{Package: "com.foo"}, t.TempDir())
		}, func(vm *ViewModel) {
			vm.apkMu.Lock()
			vm.APK = nil
			vm.apkMu.Unlock()
		}},
		{"install apk", func(vm *ViewModel) {
			vm.openInstallDialog(vm.Selection, "/tmp/示例.apk")
		}, func(vm *ViewModel) {
			vm.installMu.Lock()
			vm.APKInstall = nil
			vm.installMu.Unlock()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := newTestViewModel(t)
			vm.OpenDevice(vm.Manager.Profiles()[0].ID)
			tc.open(vm)
			tt := ui.NewTester(vm.ContentView, 900, 900)
			tt.Frame()
			tt.Frame()
			tc.close(vm)
			for i := 0; i < 10; i++ {
				tt.Advance(20 * time.Millisecond)
				tt.Frame()
			}
		})
	}
}
