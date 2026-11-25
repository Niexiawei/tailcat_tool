package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// longShellIdentity is what `id` prints for the shell user on a phone: one line
// long enough to run past the window edge if nothing wraps it.
const longShellIdentity = "uid=2000(shell) gid=2000(shell) groups=2000(shell),1004(input),1007(log),1011(adb),1015(sdcard_rw),1028(sdcard_r),3001(net_bt_admin),3002(net_bt),3003(inet),3006(net_bw_stats),3009(readproc),9997(everybody),29000(oem_29000) context=u:r:shell:s0"

// usersFixture connects a device whose `pm list users` answers with one profile
// and a work profile, so both the overview and the install dialog have two
// users to tell apart.
func usersFixture(t *testing.T) *ViewModel {
	t.Helper()
	return usersFixtureWith(t, nil)
}

func usersFixtureWith(t *testing.T, override func(joined string) (core.Result, bool)) *ViewModel {
	t.Helper()
	vm, _ := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		joined := strings.Join(c.Args, " ")
		if override != nil {
			if result, handled := override(joined); handled {
				return result, nil
			}
		}
		switch {
		case strings.Contains(joined, "get-state"):
			return core.Result{Output: "device\n"}, nil
		case strings.Contains(joined, "pm list users"):
			return core.Result{Output: "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:工作资料:404}\n@@CURRENT\n0\n"}, nil
		case strings.Contains(joined, "getprop ro.product.model"):
			// The shell identity is one long line on a real device, which is what
			// makes the overview's boxed row necessary.
			return core.Result{Output: "E245\nantora\n11\nSN123\n" + longShellIdentity + "\n"}, nil
		}
		return core.Result{}, nil
	})
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	return vm
}

func TestDeviceUserStatusWords(t *testing.T) {
	cases := []struct {
		user core.DeviceUser
		want string
	}{
		{core.DeviceUser{ID: 0, Name: "Owner", Current: true, Running: true}, "当前用户"},
		// A profile that is started but not the current one is running, not
		// current: the app must not call two users current.
		{core.DeviceUser{ID: 10, Name: "工作资料", Running: true}, "运行中"},
		{core.DeviceUser{ID: 11, Name: "访客"}, "未启动"},
	}
	for _, tc := range cases {
		if got := deviceUserStatus(tc.user); got != tc.want {
			t.Fatalf("deviceUserStatus(%+v) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

func TestOverviewShowsEveryDeviceUser(t *testing.T) {
	vm := usersFixture(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	vm.DetailTab = detailOverview
	tt := ui.NewTester(vm.ContentView, 900, 900)

	// The users are a table now: a column per attribute, one row per profile.
	for _, want := range []string{"设备用户", "用户", "名称", "状态", "0", "Owner", "10", "工作资料", "当前用户"} {
		if !tt.HasText(want) {
			t.Fatalf("the overview user table lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

// A device that will not list its users shows the placeholder, not a blank.
func TestOverviewSaysNothingWhenTheUserListIsEmpty(t *testing.T) {
	vm := usersFixtureWith(t, func(joined string) (core.Result, bool) {
		if strings.Contains(joined, "pm list users") {
			return core.Result{}, true
		}
		return core.Result{}, false
	})
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	vm.DetailTab = detailOverview
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("设备用户") || !tt.HasText(core.PlaceholderUnknown) {
		t.Fatalf("an empty user list has no placeholder; texts = %v", tt.Texts())
	}
}
