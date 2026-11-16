package app

import (
	"testing"
	"time"

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
		{"transfer", func(vm *ViewModel) { vm.TransferOpen = true }, func(vm *ViewModel) { vm.TransferOpen = false }},
		{"settings", func(vm *ViewModel) { vm.openSettings() }, func(vm *ViewModel) { vm.SettingsOpen = false }},
		{"reachability", func(vm *ViewModel) { vm.ReachabilityOpen = true }, func(vm *ViewModel) { vm.ReachabilityOpen = false }},
		{"attention", func(vm *ViewModel) { vm.AttentionOpen = true }, func(vm *ViewModel) { vm.AttentionOpen = false }},
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
