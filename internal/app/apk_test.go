package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// streamRunner answers commands and can feed the task's output sink, which is
// the only way a view test sees an `adb pull` progress line.
type streamRunner struct {
	mu     sync.Mutex
	answer func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error)
	calls  []string
}

func (r *streamRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (r *streamRunner) Run(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(c.Args, " "))
	answer := r.answer
	r.mu.Unlock()
	return answer(ctx, c, emit)
}

func (r *streamRunner) commandLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// apkAnswer is the device the APK tests talk to: one installed application with
// a base APK of 1024 bytes, and a pull that finishes immediately.
func apkAnswer(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	joined := strings.Join(c.Args, " ")
	switch {
	case strings.Contains(joined, "get-state"):
		return core.Result{Output: "device\n"}, nil
	case strings.Contains(joined, "pm list users"):
		return core.Result{Output: "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:工作资料:404}\n@@CURRENT\n0\n"}, nil
	case strings.Contains(joined, "pm list packages"):
		return core.Result{Output: "package:/data/app/~~x==/com.foo-y==/base.apk=com.foo\n"}, nil
	case strings.Contains(joined, "stat -c"):
		return core.Result{Output: "1024\n"}, nil
	}
	return core.Result{}, nil
}

// apkFixture connects one device whose adb answers through answer.
func apkFixture(t *testing.T, answer func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error)) (*ViewModel, *streamRunner) {
	t.Helper()
	if answer == nil {
		answer = apkAnswer
	}
	runner := &streamRunner{answer: answer}
	vm, _ := newRunnerViewModel(t, func(core.Command) (core.Result, error) { return core.Result{}, nil },
		func(o *core.ManagerOptions) { o.Runner = runner })
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	return vm, runner
}

// waitUntil fails the test when cond never becomes true: the APK flow does its
// expensive half in a goroutine.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// releaseOnce closes ch at most once and registers the close to run before the
// fixture's Shutdown, which waits for tasks: a blocked fake pull would hang the
// whole test otherwise.
func releaseOnce(t *testing.T, ch chan struct{}) func() {
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return release
}

// stubPickSaveFolder replaces the folder picker for one test.
func stubPickSaveFolder(fn func(title string) (string, bool)) (restore func()) {
	previous := pickSaveFolder
	pickSaveFolder = fn
	return func() { pickSaveFolder = previous }
}

func TestAPKFileNameDropsCharactersWindowsForbids(t *testing.T) {
	got := apkFileName(`微信: "内测/版"`, "com.tencent.mm")
	if strings.ContainsAny(got, `<>:"/\|?*`) {
		t.Fatalf("apkFileName() = %q, still holds a forbidden character", got)
	}
	if !strings.HasSuffix(got, "com.tencent.mm.apk") {
		t.Fatalf("apkFileName() = %q, want the package in the name", got)
	}
}

func TestAPKFileNameFallsBackToThePackage(t *testing.T) {
	if got := apkFileName("", "com.foo"); got != "com.foo.apk" {
		t.Fatalf("apkFileName(\"\", com.foo) = %q", got)
	}
	if got := apkFileName("   ", "com.foo"); got != "com.foo.apk" {
		t.Fatalf("apkFileName(\"   \", com.foo) = %q", got)
	}
}

func TestAPKFileNameKeepsALongLabelFromBecomingAPathProblem(t *testing.T) {
	got := apkFileName(strings.Repeat("很长的应用名", 20), "com.foo")
	if len([]rune(got)) > 120 {
		t.Fatalf("apkFileName() = %d runes, want a bounded name", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "com.foo.apk") {
		t.Fatalf("apkFileName() = %q", got)
	}
}

func TestUniqueAPKPathNeverOverwritesWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "com.foo.apk")
	if err := os.WriteFile(first, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "com.foo (2).apk")
	if err := os.WriteFile(second, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := uniqueAPKPath(dir, "com.foo.apk", func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	if got != filepath.Join(dir, "com.foo (3).apk") {
		t.Fatalf("uniqueAPKPath() = %q", got)
	}
}

func TestUniqueAPKPathUsesTheNameWhenNothingIsThere(t *testing.T) {
	dir := t.TempDir()
	if got := uniqueAPKPath(dir, "com.foo.apk", func(string) bool { return false }); got != filepath.Join(dir, "com.foo.apk") {
		t.Fatalf("uniqueAPKPath() = %q", got)
	}
}

func TestAPKSaveStartsOnePullOnly(t *testing.T) {
	vm, runner := apkFixture(t, nil)
	app := core.AppUsage{Package: "com.foo", Label: "示例"}
	dir := t.TempDir()
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, app, dir)
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, app, dir)
	waitUntil(t, "the pull to start", func() bool {
		for _, call := range runner.commandLog() {
			if strings.Contains(call, "pull") {
				return true
			}
		}
		return false
	})
	runs := 0
	for _, call := range runner.commandLog() {
		if strings.Contains(call, "pull") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("the APK was pulled %d times, want 1: %v", runs, runner.commandLog())
	}
}

func TestAPKSaveShowsTheProgressItReads(t *testing.T) {
	stop := make(chan struct{})
	vm, _ := apkFixture(t, func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
		if !strings.Contains(strings.Join(c.Args, " "), "pull") {
			return apkAnswer(ctx, c, emit)
		}
		// Newline-terminated: the app runs adb through a pipe, so this is what a
		// real pull prints, and the task redactor only releases whole lines.
		emit("[  0%] /data/app/~~x==/com.foo-y==/base.apk\n")
		emit("[ 42%] /data/app/~~x==/com.foo-y==/base.apk\n")
		<-stop
		return core.Result{}, nil
	})
	release := releaseOnce(t, stop)
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo", Label: "示例"}, t.TempDir())
	waitUntil(t, "the pull to report progress", func() bool {
		return strings.Contains(vm.apkTaskForTest().Output, "42%")
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	// The fake pull stays blocked until the dialog has been read; release it
	// before asserting so a failed assertion cannot stall the fixture's
	// Shutdown.
	shown := tt.Texts()
	release()
	if !textsContain(shown, "42%") {
		t.Fatalf("the dialog lost the progress it read; texts = %v", shown)
	}
	if !textsContain(shown, "正在保存") {
		t.Fatalf("the dialog does not say what it is doing; texts = %v", shown)
	}
}

// textsContain is HasText on an already captured frame.
func textsContain(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

func TestAPKProgressFallsBackToTheBytesOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "com.foo.apk")
	if err := os.WriteFile(path, make([]byte, 512), 0o600); err != nil {
		t.Fatal(err)
	}
	task := core.Task{Output: "no progress here\n"}
	if got := apkProgress(task, 1024, path); got != 0.5 {
		t.Fatalf("apkProgress() = %v, want 0.5", got)
	}
}

func TestAPKProgressIsIndeterminateWithoutAnyTotal(t *testing.T) {
	if got := apkProgress(core.Task{}, 0, filepath.Join(t.TempDir(), "missing.apk")); got != -1 {
		t.Fatalf("apkProgress() = %v, want -1", got)
	}
}

func TestAPKSaveReportsWhyThePullFailed(t *testing.T) {
	vm, _ := apkFixture(t, func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
		if strings.Contains(strings.Join(c.Args, " "), "pull") {
			return core.Result{Output: "adb: error: failed to stat remote object\n", ExitCode: 1}, nil
		}
		return apkAnswer(ctx, c, emit)
	})
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo"}, t.TempDir())
	waitUntil(t, "the pull to fail", func() bool {
		task := vm.apkTaskForTest()
		return task.ID != uuid.Nil && task.State == core.TaskFailed
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("保存失败：") {
		t.Fatalf("a failed pull shows no reason; texts = %v", tt.Texts())
	}
}

func TestAPKSaveCancelStopsTheTask(t *testing.T) {
	stop := make(chan struct{})
	vm, _ := apkFixture(t, func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
		if !strings.Contains(strings.Join(c.Args, " "), "pull") {
			return apkAnswer(ctx, c, emit)
		}
		select {
		case <-stop:
			return core.Result{}, nil
		case <-ctx.Done():
			return core.Result{Cancelled: true}, nil
		}
	})
	// Registered after the fixture, so it runs before the fixture's Shutdown:
	// cleanups are last-registered-first.
	releaseOnce(t, stop)
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo"}, t.TempDir())
	waitUntil(t, "the pull to start", func() bool {
		task := vm.apkTaskForTest()
		return task.ID != uuid.Nil && task.State == core.TaskRunning
	})
	// The dialog drops its task id when it closes, so keep it here.
	taskID := vm.apkTaskID()
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("the progress dialog has no 取消: %v", err)
	}
	waitUntil(t, "the task to be cancelled", func() bool {
		task, ok := apkTask(vm.Manager.Tasks(), taskID)
		return ok && task.State == core.TaskCancelled
	})
	if state, open := vm.apkState(); open {
		t.Fatalf("cancelling left the dialog open: %+v", state)
	}
}

func TestAPKSaveDoneShowsWhereTheFileWent(t *testing.T) {
	vm, _ := apkFixture(t, nil)
	// The reveal button is only drawn when there is something to call.
	vm.OpenDir = func(string) error { return nil }
	dir := t.TempDir()
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo", Label: "示例"}, dir)
	waitUntil(t, "the pull to finish", func() bool {
		task := vm.apkTaskForTest()
		return task.ID != uuid.Nil && task.State == core.TaskSucceeded
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	tt.Frame()
	want := filepath.Join(dir, "示例_com.foo.apk")
	if !tt.HasText("已保存到 " + want) {
		t.Fatalf("the dialog does not say where the file went; texts = %v", tt.Texts())
	}
	if !tt.HasText("打开保存目录") {
		t.Fatalf("the saved file cannot be revealed; texts = %v", tt.Texts())
	}
}

// apkTaskForTest is the pull this dialog is watching, or a zero Task.
func (vm *ViewModel) apkTaskForTest() core.Task {
	if id := vm.apkTaskID(); id != uuid.Nil {
		task, _ := apkTask(vm.Manager.Tasks(), id)
		return task
	}
	return core.Task{}
}

func TestAppRowOffersAPKDownload(t *testing.T) {
	// Clicking must never reach the real file dialog in a headless test.
	defer stubPickSaveFolder(func(string) (string, bool) { return "", false })()
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("保存应用安装包"); err != nil {
		t.Fatalf("the row has no 保存应用安装包 button: %v", err)
	}
}

// The folder is a question every time: cancelling the picker must leave no
// dialog and start nothing.
func TestAPKDownloadAlwaysAsksForAFolder(t *testing.T) {
	var asked int
	defer stubPickSaveFolder(func(string) (string, bool) { asked++; return "", false })()
	vm, runner := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("保存应用安装包"); err != nil {
		t.Fatalf("the row has no 保存应用安装包 button: %v", err)
	}
	if asked != 1 {
		t.Fatalf("the dialog asked for a folder %d times, want 1", asked)
	}
	if state, open := vm.apkState(); open {
		t.Fatalf("cancelling the picker still opened the dialog: %+v", state)
	}
	for _, call := range runner() {
		if strings.Contains(call, "pull") {
			t.Fatalf("cancelling the picker still pulled: %q", call)
		}
	}
}

// The button belongs to the row it sits on, not to whichever application the
// page happens to list first.
func TestAPKDownloadActionUsesTheRowsOwnPackage(t *testing.T) {
	defer stubPickSaveFolder(func(string) (string, bool) { return t.TempDir(), true })()
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	// Filter down to the second application so the only visible icon is its own.
	vm.resourceStateFor(profile.ID).filter = "system_server"
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("保存应用安装包"); err != nil {
		t.Fatalf("the row has no 保存应用安装包 button: %v", err)
	}
	state, open := vm.apkState()
	if !open || state.Package != "system_server" {
		t.Fatalf("the dialog opened for %+v, want system_server", state)
	}
}

// Closing while the device is still being asked where its APK is must not leave
// a pull running with nobody watching it.
func TestAPKSaveClosedWhileLocatingStartsNoPull(t *testing.T) {
	release := make(chan struct{})
	vm, runner := apkFixture(t, func(ctx context.Context, c core.Command, emit func(string)) (core.Result, error) {
		if strings.Contains(strings.Join(c.Args, " "), "pm list packages") {
			<-release
		}
		return apkAnswer(ctx, c, emit)
	})
	stop := releaseOnce(t, release)
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo"}, t.TempDir())
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("the locating dialog has no 关闭: %v", err)
	}
	if state, open := vm.apkState(); open {
		t.Fatalf("closing left the dialog open: %+v", state)
	}
	stop()
	// Give the lookup time to come back and (wrongly) start a pull.
	time.Sleep(50 * time.Millisecond)
	for _, call := range runner.commandLog() {
		if strings.Contains(call, "pull") {
			t.Fatalf("a closed dialog still started a pull: %q", call)
		}
	}
}

// Revealing the folder must be visible in the app too: on Windows the file
// manager can open behind the window, and then the user only sees that the
// button did nothing.
func TestAPKSaveRevealSaysItOpenedTheFolder(t *testing.T) {
	vm, _ := apkFixture(t, nil)
	dir := t.TempDir()
	opened := ""
	vm.OpenDir = func(path string) error { opened = path; return nil }
	vm.beginAPKSave(vm.Manager.Profiles()[0].ID, core.AppUsage{Package: "com.foo", Label: "示例"}, dir)
	waitUntil(t, "the pull to finish", func() bool {
		task := vm.apkTaskForTest()
		return task.ID != uuid.Nil && task.State == core.TaskSucceeded
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("打开保存目录"); err != nil {
		t.Fatalf("the saved dialog has no 打开保存目录: %v", err)
	}
	if opened != dir {
		t.Fatalf("the reveal asked for %q, want %q", opened, dir)
	}
	// The frame after the click drains the queue into the context, so the
	// confirmation is read off the window rather than off the queue.
	tt.Frame()
	if !tt.HasText("已打开保存目录") {
		t.Fatalf("no confirmation that the folder opened; texts = %v", tt.Texts())
	}
}
