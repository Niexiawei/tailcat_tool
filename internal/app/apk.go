package app

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// The APK export dialog's stages. There is no form stage: the folder is asked
// by the system picker before the dialog opens, because the folder is a
// question every time rather than a setting.
const (
	apkStageLocating = iota
	apkStageDownloading
	apkStageDone
	apkStageFailed
)

// apkExportTimeout is how long one export may run. A base APK is tens of
// megabytes over a slow channel, so this is generous on purpose.
const apkExportTimeout = 30 * time.Minute

// apkBaseOnlyNote is what the dialog says about split packages: an export that
// quietly produced a half-installable file would be worse than one that says
// what it is.
const apkBaseOnlyNote = "只保存设备上的基础安装包（base.apk），不含拆分包。"

// apkSave is the 「保存 APK」 dialog: which application, where it is going, and
// how far the pull got. A nil *apkSave closes the dialog.
type apkSave struct {
	Device  uuid.UUID
	Package string
	Label   string
	Dir     string
	Error   string
	Stage   int
	// APKPath and Total are what the device answered about its own file.
	APKPath string
	Total   int64
	// LocalPath is the exact file the pull writes, decided before it starts.
	LocalPath string
	TaskID    uuid.UUID
}

// pickSaveFolder asks where to put the exported APK. It is a variable so tests
// never open a real file dialog; false means the user cancelled.
var pickSaveFolder = func(title string) (string, bool) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Directory: true})
	if err != nil || len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// apkDownloadAction is the end of every row's name cell: the icon that exports
// the application's base APK. It is drawn whatever the device's state is — a
// hidden button would make the feature look absent instead of unavailable.
func (vm *ViewModel) apkDownloadAction(c *ui.Context, device uuid.UUID, app core.AppUsage) {
	if !components.IconButton(c, lucide.Download(), "保存应用安装包", components.IconButtonCompact).Clicked() {
		return
	}
	dir, ok := pickSaveFolder("选择保存 APK 的目录")
	if !ok || strings.TrimSpace(dir) == "" {
		return
	}
	vm.beginAPKSave(device, app, dir)
}

// beginAPKSave opens the progress dialog and starts the work. One dialog at a
// time: the form is not shown again until this one closes, so a second click
// cannot start a second pull.
func (vm *ViewModel) beginAPKSave(device uuid.UUID, app core.AppUsage, dir string) {
	vm.apkMu.Lock()
	if vm.APK != nil {
		vm.apkMu.Unlock()
		return
	}
	state := &apkSave{
		Device:  device,
		Package: app.Package,
		Label:   app.Label,
		Dir:     dir,
		Stage:   apkStageLocating,
	}
	vm.APK = state
	vm.apkMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		vm.failAPKSave(state, "无法创建保存目录："+err.Error())
		return
	}
	vm.requestFrame()
	go vm.runAPKSave(state)
}

// runAPKSave asks the device where its APK is and then pulls it through the
// task machinery, so the pull is redacted, cancellable and timed out like every
// other device operation.
func (vm *ViewModel) runAPKSave(state *apkSave) {
	apk, err := vm.Manager.AppAPK(context.Background(), state.Device, state.Package)
	if err != nil {
		vm.failAPKSave(state, err.Error())
		return
	}
	if _, open := vm.apkState(); !open {
		// Closed while the lookup ran: starting the pull now would leave a task
		// nobody is watching.
		return
	}
	local := uniqueAPKPath(state.Dir, apkFileName(state.Label, state.Package), fileExists)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), state.Device,
		"保存 APK "+state.Package, []string{"pull", apk.Path, local}, apkExportTimeout)
	if err != nil {
		vm.failAPKSave(state, err.Error())
		return
	}
	vm.apkMu.Lock()
	if vm.APK == state {
		state.APKPath, state.Total, state.LocalPath, state.TaskID = apk.Path, apk.Size, local, taskID
		state.Stage = apkStageDownloading
		vm.apkMu.Unlock()
		vm.requestFrame()
		return
	}
	vm.apkMu.Unlock()
	// Closed between the check above and the task starting.
	vm.Manager.CancelTask(taskID)
}

func (vm *ViewModel) failAPKSave(state *apkSave, reason string) {
	vm.apkMu.Lock()
	if vm.APK == state {
		state.Stage = apkStageFailed
		state.Error = reason
	}
	vm.apkMu.Unlock()
	vm.requestFrame()
}

// closeAPKSave drops the dialog; a running pull is cancelled first, so closing
// never leaves a task nobody is watching.
func (vm *ViewModel) closeAPKSave(state *apkSave) {
	vm.apkMu.Lock()
	taskID := state.TaskID
	if vm.APK == state {
		vm.APK = nil
	}
	vm.apkMu.Unlock()
	if taskID != uuid.Nil {
		vm.Manager.CancelTask(taskID)
	}
}

// APKView renders the export dialog: what it is doing, how far it got, and the
// one thing the user can do about it right now.
func (vm *ViewModel) APKView(c *ui.Context) {
	vm.apkMu.Lock()
	state := vm.APK
	var stage int
	var dir, localPath, errText string
	var total int64
	var taskID uuid.UUID
	if state != nil {
		stage, dir, localPath, errText = state.Stage, state.Dir, state.LocalPath, state.Error
		total, taskID = state.Total, state.TaskID
	}
	vm.apkMu.Unlock()

	var task core.Task
	found := false
	if taskID != uuid.Nil {
		task, found = apkTask(vm.Manager.Tasks(), taskID)
	}
	if found {
		switch task.State {
		case core.TaskSucceeded:
			stage = apkStageDone
		case core.TaskFailed, core.TaskCancelled:
			stage = apkStageFailed
			if text := strings.TrimSpace(task.Output); text != "" {
				errText = text
			} else if errText == "" {
				errText = "任务已取消"
			}
		default:
			stage = apkStageDownloading
		}
	}

	open := state != nil
	components.Dialog(c, &open, components.DialogOptions{Label: "保存 APK"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(480)
		panel.Children(func() {
			components.Text(c, "保存 APK").FontSize(18).Bold()
			switch stage {
			case apkStageLocating:
				components.Text(c, "正在查找应用安装包…").FontSize(11)
				components.Progress(c, -1).Label("正在查找应用安装包")
				components.Text(c, apkBaseOnlyNote).FontSize(11).TextColor(c.Theme().TextMuted)
			case apkStageDownloading:
				components.Text(c, "正在保存 "+fileName(localPath)).FontSize(11)
				components.Progress(c, apkProgress(task, total, localPath)).Label("保存 APK 的进度")
				if text := apkProgressText(task, total, localPath); text != "" {
					components.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted)
				}
				components.Text(c, apkBaseOnlyNote).FontSize(11).TextColor(c.Theme().TextMuted)
			case apkStageDone:
				components.Text(c, "已保存到 "+localPath).FontSize(11)
				components.Progress(c, 1).Label("保存 APK 的进度")
			default:
				components.Text(c, "保存失败："+errText).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if stage == apkStageDownloading {
					if components.Button(c, "取消", components.ButtonOutline).Clicked() {
						vm.closeAPKSave(state)
						open = false
					}
					return
				}
				if stage == apkStageDone && vm.OpenDir != nil {
					if components.Button(c, "打开保存目录", components.ButtonOutline).Clicked() {
						if err := vm.OpenDir(dir); err != nil {
							vm.Fail(err)
						} else {
							// Say it happened: the file manager can open behind this
							// window, which otherwise reads as a dead button.
							vm.Notify("已打开保存目录")
						}
					}
				}
				choice := components.ButtonOutline
				if stage != apkStageLocating {
					choice = components.ButtonPrimary
				}
				if components.Button(c, "关闭", choice).Clicked() {
					vm.closeAPKSave(state)
					open = false
				}
			})
		})
	})
	if !open && vm.APK != nil {
		vm.closeAPKSave(state)
	}
}

// apkState is a copy of the dialog state, taken under the lock: the goroutine
// that runs the export keeps writing to the real one.
func (vm *ViewModel) apkState() (apkSave, bool) {
	vm.apkMu.Lock()
	defer vm.apkMu.Unlock()
	if vm.APK == nil {
		return apkSave{}, false
	}
	return *vm.APK, true
}

// apkTaskID is the pull the dialog is watching; uuid.Nil when there is none.
func (vm *ViewModel) apkTaskID() uuid.UUID {
	vm.apkMu.Lock()
	defer vm.apkMu.Unlock()
	if vm.APK == nil {
		return uuid.Nil
	}
	return vm.APK.TaskID
}

// apkTask finds the pull this dialog is watching.
func apkTask(tasks []core.Task, id uuid.UUID) (core.Task, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return core.Task{}, false
}

// apkProgress is 0..1 when anything is known, and -1 (indeterminate) when it is
// not: adb only prints percentages through some versions and pipes, so the
// bytes already on disk are the fallback and "nothing known" is not zero.
func apkProgress(task core.Task, total int64, localPath string) float64 {
	if percent, ok := core.ParseTransferPercent(task.Output); ok {
		return float64(percent) / 100
	}
	if total <= 0 {
		return -1
	}
	written := fileSize(localPath)
	if written <= 0 {
		return 0
	}
	return math.Min(1, float64(written)/float64(total))
}

// apkProgressText is the sentence under the bar.
func apkProgressText(task core.Task, total int64, localPath string) string {
	if percent, ok := core.ParseTransferPercent(task.Output); ok {
		return strconv.Itoa(percent) + "%"
	}
	if total <= 0 {
		return ""
	}
	written := uint64(max(0, fileSize(localPath)))
	return "已传输 " + core.FormatBytes(written) + " / " + core.FormatBytes(uint64(total))
}

// apkLabelLimit is how much of the application's own name a filename keeps.
const apkLabelLimit = 60

// apkFileName is what the saved file is called: the name the user recognises,
// then the package so two builds never collide. Characters Windows forbids in a
// filename are replaced, not dropped, so two names stay distinguishable.
func apkFileName(label, pkg string) string {
	name := sanitizeAPKLabel(label)
	if name == "" {
		return pkg + ".apk"
	}
	return name + "_" + pkg + ".apk"
}

func sanitizeAPKLabel(label string) string {
	var b strings.Builder
	kept := 0
	for _, r := range strings.TrimSpace(label) {
		if kept >= apkLabelLimit {
			break
		}
		switch {
		case r < 0x20, r == 0x7f, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
		kept++
	}
	return strings.TrimRight(strings.TrimSpace(b.String()), ". ")
}

// apkNameAttempts bounds how many numbered names one export tries before it
// falls back to the clock: a directory holding a thousand copies of one APK is
// already broken in a way numbering will not fix.
const apkNameAttempts = 999

// uniqueAPKPath puts name into dir, adding " (2)", " (3)" … before the
// extension until the path is free. Exporting is a backup: it must never
// overwrite a file the user already has.
func uniqueAPKPath(dir, name string, exists func(string) bool) string {
	path := filepath.Join(dir, name)
	if !exists(path) {
		return path
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i <= apkNameAttempts; i++ {
		candidate := filepath.Join(dir, stem+" ("+strconv.Itoa(i)+")"+ext)
		if !exists(candidate) {
			return candidate
		}
	}
	return filepath.Join(dir, stem+" ("+strconv.FormatInt(time.Now().UnixNano(), 10)+")"+ext)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
