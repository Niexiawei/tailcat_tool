package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestInstallArgsTargetTheChosenUser(t *testing.T) {
	args := installArgs("/tmp/app.apk", 10, true)
	want := []string{"install", "-r", "--user", "10", "/tmp/app.apk"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("installArgs() = %v, want %v", args, want)
	}
}

// Without a user the command is exactly today's: a device that will not list
// its profiles must keep working the way it did.
func TestInstallArgsWithoutAUserKeepTodaysCommand(t *testing.T) {
	args := installArgs("/tmp/app.apk", 0, false)
	want := []string{"install", "-r", "/tmp/app.apk"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("installArgs() = %v, want %v", args, want)
	}
}

func TestInstallUserLabelMarksTheCurrentUser(t *testing.T) {
	current := installUserLabel(core.DeviceUser{ID: 0, Name: "Owner", Current: true})
	if current != "0 · Owner（当前）" {
		t.Fatalf("installUserLabel() = %q", current)
	}
	other := installUserLabel(core.DeviceUser{ID: 10, Name: "工作资料"})
	if other != "10 · 工作资料" {
		t.Fatalf("installUserLabel() = %q", other)
	}
}

func TestInstallDialogOffersEveryUser(t *testing.T) {
	vm := usersFixture(t)
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	for _, want := range []string{
		"安装 APK",
		"APK 文件",
		"/tmp/示例.apk",
		"选择 APK 文件",
		"覆盖安装：同包名的应用会被替换，应用数据保留。",
		"安装到用户",
		"0 · Owner（当前）",
	} {
		if !tt.HasText(want) {
			t.Fatalf("the install dialog lacks %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestInstallDialogSaysWhichUserItWillUse(t *testing.T) {
	vm := usersFixture(t)
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	state, open := vm.installSnapshot()
	if !open || !state.Chosen || state.UserID != 0 {
		t.Fatalf("install draft = %+v, want the device's current user chosen", state)
	}
}

// A device that will not list its users still installs: to the default user,
// and the dialog says so instead of pretending it has a choice.
func TestInstallFallsBackToTheDefaultUserWhenTheListFails(t *testing.T) {
	vm, runner := apkFixture(t, func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
		if strings.Contains(strings.Join(c.Args, " "), "pm list users") {
			return core.Result{Output: "Error: java.lang.SecurityException\n", ExitCode: 1}, nil
		}
		return apkAnswer(ctx, c, emit)
	})
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to fail", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading && state.Err != ""
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if !tt.HasText("读不到设备上的用户列表，将安装到默认用户") {
		t.Fatalf("the dialog hides that it cannot choose; texts = %v", tt.Texts())
	}
	if err := tt.Click("安装"); err != nil {
		t.Fatalf("the install dialog has no 安装 button: %v", err)
	}
	waitUntil(t, "the install task", func() bool {
		for _, call := range runner.commandLog() {
			if strings.Contains(call, " install ") {
				return true
			}
		}
		return false
	})
	for _, call := range runner.commandLog() {
		if strings.Contains(call, " install ") && strings.Contains(call, "--user") {
			t.Fatalf("an unreadable user list still passed --user: %q", call)
		}
	}
}

func TestInstallStartsOneTaskOnly(t *testing.T) {
	vm, runner := apkFixture(t, nil)
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("安装"); err != nil {
		t.Fatalf("the install dialog has no 安装 button: %v", err)
	}
	if state, open := vm.installSnapshot(); open {
		vm.submitInstall(&state)
	}
	waitUntil(t, "the install task", func() bool {
		for _, call := range runner.commandLog() {
			if strings.Contains(call, " install ") {
				return true
			}
		}
		return false
	})
	runs := 0
	for _, call := range runner.commandLog() {
		if strings.Contains(call, " install ") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("the APK was installed %d times, want 1: %v", runs, runner.commandLog())
	}
	if state, open := vm.installSnapshot(); open {
		t.Fatalf("a started install left the dialog open: %+v", state)
	}
}

func TestInstallDialogCancelStartsNothing(t *testing.T) {
	vm, runner := apkFixture(t, nil)
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("the install dialog has no 取消 button: %v", err)
	}
	if state, open := vm.installSnapshot(); open {
		t.Fatalf("cancel left the dialog open: %+v", state)
	}
	for _, call := range runner.commandLog() {
		if strings.Contains(call, " install ") {
			t.Fatalf("cancel still installed: %q", call)
		}
	}
}

// 安装 APK opens the dialog first: the file is chosen inside it, not before it.
func TestChooseAPKOpensTheDialogBeforeAskingForAFile(t *testing.T) {
	vm, _ := apkFixture(t, nil)
	asked := 0
	defer stubPickAPKFile(func(string) (string, bool) {
		asked++
		return "/tmp/示例.apk", true
	})()
	vm.chooseAPK(vm.Manager.Profiles()[0])
	if asked != 0 {
		t.Fatalf("the file picker ran %d times before the dialog appeared", asked)
	}
	state, open := vm.installSnapshot()
	if !open {
		t.Fatal("chooseAPK() opened no dialog")
	}
	if state.Path != "" {
		t.Fatalf("the dialog opened with a path the user never chose: %q", state.Path)
	}
}

// The dialog's own button picks the file and then shows where it is.
func TestInstallDialogPicksTheAPKAndShowsItsPath(t *testing.T) {
	vm, _ := apkFixture(t, nil)
	const chosen = `C:\Users\me\Downloads\示例应用_1.2.3.apk`
	defer stubPickAPKFile(func(title string) (string, bool) {
		if !strings.Contains(title, "APK") {
			t.Fatalf("the picker title = %q, want it to say APK", title)
		}
		return chosen, true
	})()
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if !tt.HasText("未选择 APK 文件") {
		t.Fatalf("the dialog does not say it has no file; texts = %v", tt.Texts())
	}
	if err := tt.Click("选择 APK 文件"); err != nil {
		t.Fatalf("the dialog has no 选择 APK 文件 button: %v", err)
	}
	tt.Frame()
	if !tt.HasText(chosen) {
		t.Fatalf("the chosen path is not shown; texts = %v", tt.Texts())
	}
}

// Without a file there is nothing to install, and pressing 安装 must not start
// a task that can only fail.
func TestInstallWaitsForAFileBeforeItStarts(t *testing.T) {
	vm, runner := apkFixture(t, nil)
	vm.openInstallDialog(vm.Manager.Profiles()[0].ID, "")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	// Disabled or guarded, pressing it must not start an install of nothing.
	_ = tt.Click("安装")
	for _, call := range runner.commandLog() {
		if strings.Contains(call, " install ") {
			t.Fatalf("an install ran with no file: %q", call)
		}
	}
	if _, open := vm.installSnapshot(); !open {
		t.Fatal("the dialog closed with no file chosen")
	}
}

// stubPickAPKFile replaces the file picker for one test.
func stubPickAPKFile(fn func(title string) (string, bool)) (restore func()) {
	previous := pickAPKFile
	pickAPKFile = fn
	return func() { pickAPKFile = previous }
}
