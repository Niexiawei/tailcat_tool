package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

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
			return core.Result{Output: "E245\nantora\n11\nSN123\nuid=0(root)\n"}, nil
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

func TestDeviceUsersTextListsEveryProfile(t *testing.T) {
	users := []core.DeviceUser{{ID: 0, Name: "Owner", Current: true}, {ID: 10, Name: "工作资料"}}
	if got := deviceUsersText(users); got != "0 Owner（当前） · 10 工作资料" {
		t.Fatalf("deviceUsersText() = %q", got)
	}
}

func TestDeviceUsersTextIsAPlaceholderWhenNothingWasRead(t *testing.T) {
	if got := deviceUsersText(nil); got != core.PlaceholderUnknown {
		t.Fatalf("deviceUsersText(nil) = %q, want %q", got, core.PlaceholderUnknown)
	}
}

func TestOverviewShowsEveryDeviceUser(t *testing.T) {
	vm := usersFixture(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	vm.DetailTab = detailOverview
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if !tt.HasText("设备用户") {
		t.Fatalf("the overview has no 设备用户 row; texts = %v", tt.Texts())
	}
	if !tt.HasText("0 Owner（当前） · 10 工作资料") {
		t.Fatalf("the overview lost the device users; texts = %v", tt.Texts())
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
