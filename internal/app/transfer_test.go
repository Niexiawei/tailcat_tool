package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// streamingRunner answers adb like recordingRunner, but it also streams a
// scripted progress line and can hold the command open until its context ends.
type streamingRunner struct {
	progress string
	result   core.Result
	hold     bool
}

func (r *streamingRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (r *streamingRunner) Run(ctx context.Context, c core.Command, onOutput func(string)) (core.Result, error) {
	batch := false
	for _, arg := range c.Args {
		switch arg {
		case "get-state":
			return core.Result{Output: "device\n"}, nil
		case "connect", "disconnect":
			// Only the batch commands are held open: connecting must finish, or
			// there is no device for the batch to run on.
			return core.Result{Output: "connected\n"}, nil
		case "push", "pull", "install":
			batch = true
		}
	}
	if !batch {
		return core.Result{}, nil
	}
	if r.progress != "" && onOutput != nil {
		onOutput(r.progress)
	}
	if r.hold {
		<-ctx.Done()
		return core.Result{Cancelled: true}, nil
	}
	return r.result, nil
}

// newStreamingViewModel builds a connected device whose adb answers with runner.
func newStreamingViewModel(t *testing.T, runner *streamingRunner) *ViewModel {
	t.Helper()
	vm := newViewModelWithRunner(t, runner)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	vm.DetailTab = detailFiles
	vm.OpenDevice(profile.ID)
	return vm
}

// prepareTransfer opens the dialog and fills it the way the form would, without
// typing into text boxes the tester cannot drive.
func prepareTransfer(t *testing.T, vm *ViewModel, upload bool, locals []string, remote string) {
	t.Helper()
	profile := vm.Manager.Profiles()[0]
	if upload {
		vm.chooseUpload(profile)
	} else {
		vm.startDownload(profile)
	}
	vm.transferMu.Lock()
	draft := vm.Transfer
	draft.LocalPaths, draft.RemotePath, draft.Stage = locals, remote, transferStageForm
	vm.transferMu.Unlock()
}

// transferTaskIDs reads the tasks the dialog is following.
func transferTaskIDs(vm *ViewModel) []string {
	vm.transferMu.Lock()
	defer vm.transferMu.Unlock()
	if vm.Transfer == nil {
		return nil
	}
	ids := make([]string, 0, len(vm.Transfer.TaskIDs))
	for _, id := range vm.Transfer.TaskIDs {
		ids = append(ids, id.String())
	}
	return ids
}

// taskStateOf is the state of one task, or "" when it is gone.
func taskStateOf(vm *ViewModel, id string) core.TaskState {
	for _, task := range vm.Manager.Tasks() {
		if task.ID.String() == id {
			return task.State
		}
	}
	return ""
}

// 本地路径在弹窗里选：打开弹窗什么也没选，点「选择文件…」才填上。
func TestUploadDialogPicksItsLocalFilesInsideTheDialog(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	openLocalPaths = func(mygo.OpenDialogOptions) ([]string, error) {
		return []string{"/tmp/update.zip"}, nil
	}
	t.Cleanup(func() { openLocalPaths = mygo.Dialog.Open })

	vm.chooseUpload(profile)
	draft, open := vm.transferSnapshot()
	if !open || !draft.Upload || len(draft.LocalPaths) != 0 {
		t.Fatalf("the dialog should open empty: %+v, %v", draft, open)
	}

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("选择文件…"); err != nil {
		t.Fatalf("no 选择文件… button: %v", err)
	}
	draft, _ = vm.transferSnapshot()
	if len(draft.LocalPaths) != 1 || draft.LocalPaths[0] != "/tmp/update.zip" {
		t.Fatalf("local paths = %v", draft.LocalPaths)
	}
}

// 「浏览设备…」把同一个弹窗切到选择阶段，选中的是设备上的目录。
func TestUploadDialogBrowsesTheDeviceForItsTargetFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.chooseUpload(profile)

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("浏览设备…"); err != nil {
		t.Fatalf("no 浏览设备… button: %v", err)
	}
	waitForText(t, tt, "Download")
	if err := tt.Click("Download"); err != nil {
		t.Fatalf("the picker has no Download row: %v", err)
	}
	waitForText(t, tt, "update.zip")
	if err := tt.Click("选择此文件夹"); err != nil {
		t.Fatalf("no 选择此文件夹 button: %v", err)
	}
	draft, _ := vm.transferSnapshot()
	if draft.RemotePath != "/sdcard/Download" {
		t.Fatalf("remote path = %q", draft.RemotePath)
	}
	if draft.Stage != transferStageForm {
		t.Fatalf("the picker did not return to the form: %d", draft.Stage)
	}
}

// 下载的来源可以直接点一个文件。
func TestDownloadDialogPicksAFileFromTheDevice(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.startDownload(profile)

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("浏览设备…"); err != nil {
		t.Fatalf("no 浏览设备… button: %v", err)
	}
	waitForText(t, tt, "照片.jpg")
	if err := tt.Click("照片.jpg"); err != nil {
		t.Fatalf("the picker has no 照片.jpg row: %v", err)
	}
	draft, _ := vm.transferSnapshot()
	if draft.RemotePath != "/sdcard/照片.jpg" || draft.Stage != transferStageForm {
		t.Fatalf("draft = %+v", draft)
	}
}

// 两个路径没齐时不能开始。
func TestTransferStartIsDisabledWithoutBothPaths(t *testing.T) {
	vm, commands := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.chooseUpload(profile) // 没选本地文件

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	_ = tt.Click("开始") // the button is disabled; a click must do nothing
	draft, _ := vm.transferSnapshot()
	if draft.Stage != transferStageForm {
		t.Fatalf("开始 started a batch without a local file: %+v", draft)
	}
	for _, call := range commands() {
		if strings.Contains(call, " push ") {
			t.Fatalf("a push ran without both paths: %q", call)
		}
	}
}

// 开始之后进度就在这个弹窗里。
func TestTransferShowsProgressInTheSameDialog(t *testing.T) {
	vm := newStreamingViewModel(t, &streamingRunner{progress: "[ 42%] /sdcard/Download/update.zip\n", hold: true})
	openLocalPaths = func(mygo.OpenDialogOptions) ([]string, error) { return []string{"/tmp/update.zip"}, nil }
	t.Cleanup(func() { openLocalPaths = mygo.Dialog.Open })

	prepareTransfer(t, vm, true, []string{"/tmp/update.zip"}, "/sdcard/Download/")
	vm.submitTransfer()

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "42%")
	for _, want := range []string{"上传 update.zip", "操作进度", "取消", "关闭"} {
		if !tt.HasText(want) {
			t.Fatalf("the running dialog has no %q; texts = %v", want, tt.Texts())
		}
	}
}

// 关窗不杀任务（D2）。
func TestTransferCloseWhileRunningKeepsTheTask(t *testing.T) {
	vm := newStreamingViewModel(t, &streamingRunner{hold: true})
	prepareTransfer(t, vm, true, []string{"/tmp/update.zip"}, "/sdcard/Download/")
	vm.submitTransfer()
	id := transferTaskIDs(vm)
	if len(id) != 1 {
		t.Fatalf("tasks = %v", id)
	}

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	vm.closeTransfer()
	tt.Frame()

	if _, open := vm.transferSnapshot(); open {
		t.Fatal("the dialog stayed open")
	}
	if got := taskStateOf(vm, id[0]); got != core.TaskRunning {
		t.Fatalf("closing the dialog killed the task: %q", got)
	}
}

// 取消才杀任务。
func TestTransferCancelStopsTheTask(t *testing.T) {
	vm := newStreamingViewModel(t, &streamingRunner{hold: true})
	prepareTransfer(t, vm, true, []string{"/tmp/update.zip"}, "/sdcard/Download/")
	vm.submitTransfer()
	id := transferTaskIDs(vm)

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	waitForText(t, tt, "取消")
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("no 取消 button: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for taskStateOf(vm, id[0]) == core.TaskRunning {
		if time.Now().After(deadline) {
			t.Fatalf("the task is still running: %q", taskStateOf(vm, id[0]))
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, open := vm.transferSnapshot(); open {
		t.Fatal("取消 left the dialog open")
	}
}

// 选择器里点表格中的文件夹行要能进去（和文件页一样），不能只有左侧的树能动。
func TestPickerOpensAFolderFromItsTable(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	picker := vm.pickerStateFor(profile.ID)
	vm.showDir(profile, picker, "/")
	// 折叠根，树里就没有同名的子行了：点击只会落在表格那一行上。
	picker.tree.Open.Remove("/")

	tt := ui.NewTester(func(c *ui.Context) {
		vm.pickerPane(c, profile, picker, pickFolder, nil)
	}, 760, 420)
	waitForText(t, tt, "system")
	if err := tt.Click("system"); err != nil {
		t.Fatalf("the picker table has no system row: %v", err)
	}
	tt.Frame()
	if picker.path != "/system" {
		t.Fatalf("clicking a folder row did not open it: %q", picker.path)
	}
}

// 设备没连上时，选择阶段说清原因，而不是永远转圈。
func TestTransferPickerSaysItNeedsAConnection(t *testing.T) {
	vm := newTestViewModel(t) // 未连接
	vm.openTransfer(vm.Manager.Profiles()[0].ID, true)
	vm.browseTransferDevice()

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if !tt.HasText("连接后才能浏览设备文件。") {
		t.Fatalf("the picker does not say the device is offline; texts = %v", tt.Texts())
	}
	if tt.HasText("正在读取…") {
		t.Fatalf("an offline picker is still spinning; texts = %v", tt.Texts())
	}
}
