package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// ValidateRemotePath rejects anything that cannot be an absolute device path.
func ValidateRemotePath(path string) error {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return core.Error("请填写绝对远端路径，例如 /sdcard/Download/example.txt")
	}
	if strings.ContainsAny(path, "\x00\n") {
		return core.Error("远端路径不能包含换行或空字符")
	}
	return nil
}

// transferTimeout bounds one push or pull. A folder of photos through a relay
// is not fast, and the task stays cancellable meanwhile.
const transferTimeout = 30 * time.Minute

// The transfer dialog's stages. The result of a finished batch is read off the
// task list, the way 「保存 APK」 does it, so no goroutine has to write a stage.
const (
	transferStageForm = iota
	transferStageBrowsing
	transferStageRunning
)

// transferDraft is the 「上传文件」/「下载文件」 dialog: what the user chose, where
// it is browsing the device, and the tasks it follows. A nil *transferDraft
// closes it.
type transferDraft struct {
	Device uuid.UUID
	Upload bool
	Stage  int
	// LocalPaths is one or more local files/folders for an upload, and exactly
	// one local destination for a download.
	LocalPaths []string
	// RemotePath is the device side: the destination folder of an upload, the
	// source of a download.
	RemotePath string
	// PickerMode says what the browsing stage may choose.
	PickerMode pickerMode
	// Err explains the last refusal (a bad path, a task that would not start).
	Err     string
	TaskIDs []uuid.UUID
	Started time.Time
}

// batch is the progress block's view of this draft.
func (d *transferDraft) batch() taskBatch {
	kind := taskPush
	title := "上传"
	if d.Upload {
		switch len(d.LocalPaths) {
		case 0:
			title = "上传文件"
		case 1:
			title = "上传 " + fileName(d.LocalPaths[0])
		default:
			title = "上传 " + itoa(len(d.LocalPaths)) + " 个文件"
		}
	} else {
		kind = taskPull
		title = "下载 " + fileName(d.RemotePath)
	}
	batch := taskBatch{Device: d.Device, Title: title, Kind: kind, Tasks: d.TaskIDs, Started: d.Started}
	if !d.Upload && len(d.LocalPaths) == 1 {
		// A single file has a size the bar can fall back to when adb prints no
		// percentage; a directory's number is not progress.
		if info, err := os.Stat(d.LocalPaths[0]); err == nil && !info.IsDir() {
			batch.LocalPath = d.LocalPaths[0]
		}
	}
	return batch
}

// transferSnapshot is a copy of the dialog state, taken under the lock.
func (vm *ViewModel) transferSnapshot() (transferDraft, bool) {
	vm.transferMu.Lock()
	defer vm.transferMu.Unlock()
	if vm.Transfer == nil {
		return transferDraft{}, false
	}
	return *vm.Transfer, true
}

// openTransfer shows the dialog with empty paths: the user picks both inside it.
func (vm *ViewModel) openTransfer(device uuid.UUID, upload bool) {
	vm.transferMu.Lock()
	vm.Transfer = &transferDraft{
		Device: device,
		Upload: upload,
		Stage:  transferStageForm,
		// /sdcard is the one folder every Android device has and the shell can
		// write to; the user still edits it or picks another one by browsing.
		RemotePath: "/sdcard",
		PickerMode: pickModeFor(upload),
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

func pickModeFor(upload bool) pickerMode {
	if upload {
		return pickFolder
	}
	return pickEntry
}

// closeTransfer drops the dialog. A running task keeps running: closing is not
// cancelling, and 取消 is the button that is.
func (vm *ViewModel) closeTransfer() {
	vm.transferMu.Lock()
	vm.Transfer = nil
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// cancelTransfer stops every task the dialog started, then closes it.
func (vm *ViewModel) cancelTransfer() {
	vm.transferMu.Lock()
	draft := vm.Transfer
	var ids []uuid.UUID
	if draft != nil {
		ids = append(ids, draft.TaskIDs...)
	}
	vm.Transfer = nil
	vm.transferMu.Unlock()
	for _, id := range ids {
		vm.Manager.CancelTask(id)
	}
	vm.requestFrame()
}

// browseTransferDevice points the same dialog at the device browser. It starts
// where the file page is, so a user reading a folder does not have to walk back
// down to it.
func (vm *ViewModel) browseTransferDevice() {
	vm.transferMu.Lock()
	draft := vm.Transfer
	vm.transferMu.Unlock()
	if draft == nil {
		return
	}
	profile, ok := vm.profileByID(draft.Device.String())
	if !ok {
		return
	}
	start := filePageRoot
	if page, ok := vm.files[filePagePane(draft.Device)]; ok && strings.HasPrefix(page.path, "/") {
		start = page.path
	}
	state := vm.syncPaneState(filePickerPane(draft.Device), vm.Manager.Session(draft.Device).ConnID)
	vm.showDir(profile, state, start)

	vm.transferMu.Lock()
	draft.Stage = transferStageBrowsing
	draft.PickerMode = pickModeFor(draft.Upload)
	draft.Err = ""
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// leaveBrowsing returns to the form, keeping whatever was already chosen.
func (vm *ViewModel) leaveBrowsing() {
	vm.transferMu.Lock()
	if vm.Transfer != nil {
		vm.Transfer.Stage = transferStageForm
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// choosePickerEntry is what clicking a row does in a download's picker: a file
// is the thing to pull, so it goes straight into the path field.
func (vm *ViewModel) choosePickerEntry(entry core.RemoteEntry) {
	vm.transferMu.Lock()
	draft := vm.Transfer
	if draft == nil || draft.Upload {
		// An upload target is always a folder; a file click means nothing.
		vm.transferMu.Unlock()
		return
	}
	draft.RemotePath = entry.Path
	draft.Stage = transferStageForm
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// choosePickerFolder takes the picker's current folder.
func (vm *ViewModel) choosePickerFolder() {
	vm.transferMu.Lock()
	draft := vm.Transfer
	vm.transferMu.Unlock()
	if draft == nil {
		return
	}
	state := vm.paneStateFor(filePickerPane(draft.Device))
	vm.transferMu.Lock()
	draft.RemotePath = state.path
	draft.Stage = transferStageForm
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// pickTransferFiles sets the local files an upload sends.
func (vm *ViewModel) pickTransferFiles() {
	paths, err := openLocalPaths(mygo.OpenDialogOptions{
		Title:    "选择要上传的文件",
		Multiple: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	vm.transferMu.Lock()
	if draft := vm.Transfer; draft != nil {
		draft.LocalPaths = paths
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// pickTransferFolder makes one local folder the thing an upload sends.
func (vm *ViewModel) pickTransferFolder() {
	paths, err := openLocalPaths(mygo.OpenDialogOptions{
		Title:     "选择要上传的文件夹",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	vm.transferMu.Lock()
	if draft := vm.Transfer; draft != nil {
		draft.LocalPaths = []string{paths[0]}
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// pickDownloadFolder asks where a pulled file or folder lands.
func (vm *ViewModel) pickDownloadFolder() {
	paths, err := openLocalPaths(mygo.OpenDialogOptions{
		Title:     "选择保存位置",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	vm.transferMu.Lock()
	if draft := vm.Transfer; draft != nil {
		draft.LocalPaths = []string{paths[0]}
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// failTransfer keeps the form open and says why nothing started.
func (vm *ViewModel) failTransfer(reason string) {
	vm.transferMu.Lock()
	if draft := vm.Transfer; draft != nil {
		draft.Stage = transferStageForm
		draft.Err = reason
	}
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// submitTransfer starts the tasks and switches the dialog to its running stage.
// The paths are validated before anything starts, and BeginADBTask snapshots the
// device's target, so switching devices never retargets a running batch.
func (vm *ViewModel) submitTransfer() {
	vm.transferMu.Lock()
	draft := vm.Transfer
	vm.transferMu.Unlock()
	if draft == nil {
		return
	}
	if _, ok := vm.profileByID(draft.Device.String()); !ok {
		vm.failTransfer("设备不存在")
		return
	}
	remote := strings.TrimSpace(draft.RemotePath)
	if err := ValidateRemotePath(remote); err != nil {
		vm.failTransfer(err.Error())
		return
	}
	var ids []uuid.UUID
	if draft.Upload {
		if len(draft.LocalPaths) == 0 {
			vm.failTransfer("请选择要上传的本地文件")
			return
		}
		for _, local := range draft.LocalPaths {
			id, err := vm.Manager.BeginADBTask(context.Background(), draft.Device,
				"上传 "+fileName(local), []string{"push", local, remote}, transferTimeout)
			if err != nil {
				vm.failTransfer(err.Error())
				return
			}
			ids = append(ids, id)
		}
	} else {
		if len(draft.LocalPaths) != 1 {
			vm.failTransfer("请选择本地保存位置")
			return
		}
		id, err := vm.Manager.BeginADBTask(context.Background(), draft.Device,
			"下载 "+fileName(remote), []string{"pull", remote, draft.LocalPaths[0]}, transferTimeout)
		if err != nil {
			vm.failTransfer(err.Error())
			return
		}
		ids = append(ids, id)
	}
	vm.transferMu.Lock()
	draft.TaskIDs = ids
	draft.Started = time.Now()
	draft.Stage = transferStageRunning
	draft.Err = ""
	vm.transferMu.Unlock()
	vm.requestFrame()
}

// beginTransferFromPage starts a batch both of whose paths the page already has:
// its own pickers were the question, so there is nothing left to ask.
func (vm *ViewModel) beginTransferFromPage(device uuid.UUID, upload bool, locals []string, remote string) {
	vm.transferMu.Lock()
	vm.Transfer = &transferDraft{
		Device:     device,
		Upload:     upload,
		Stage:      transferStageForm,
		LocalPaths: locals,
		RemotePath: remote,
		PickerMode: pickModeFor(upload),
	}
	vm.transferMu.Unlock()
	vm.submitTransfer()
}

// TransferView renders the upload/download dialog: the form, the device picker,
// the running batch and its result are the states of one dialog.
func (vm *ViewModel) TransferView(c *ui.Context) {
	vm.transferMu.Lock()
	draft := vm.Transfer
	vm.transferMu.Unlock()
	if draft == nil {
		return
	}
	title := "下载文件"
	if draft.Upload {
		title = "上传文件"
	}
	open := true
	components.Dialog(c, &open, components.DialogOptions{Label: title}, func(panel ui.Element) {
		width := float32(460)
		if draft.Stage == transferStageBrowsing {
			width = 760
		}
		panel.Padding(18).Gap(10).Width(width)
		panel.Children(func() {
			components.Text(c, title).FontSize(18).Bold()
			switch draft.Stage {
			case transferStageBrowsing:
				profile, ok := vm.profileByID(draft.Device.String())
				if !ok {
					vm.closeTransfer()
					return
				}
				session := vm.Manager.Session(draft.Device)
				if session.State != core.StateOnline {
					// Same rule as the file page: an empty table would read as
					// an empty device, and a spinner would never stop.
					vm.filesOffline(c, session)
				} else {
					state := vm.syncPaneState(filePickerPane(draft.Device), session.ConnID)
					vm.pickerPane(c, profile, state, draft.PickerMode, vm.choosePickerEntry)
				}
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					if components.Button(c, "返回", components.ButtonOutline).Clicked() {
						vm.leaveBrowsing()
					}
					if components.Button(c, "选择此文件夹", components.ButtonPrimary).Clicked() {
						vm.choosePickerFolder()
					}
				})
			case transferStageRunning:
				vm.transferResult(c, draft)
			default:
				vm.transferForm(c, draft)
			}
		})
	})
	if !open {
		vm.closeTransfer()
	}
}

// transferForm is the first stage: both paths, and the ways to pick each.
func (vm *ViewModel) transferForm(c *ui.Context, draft *transferDraft) {
	theme := c.Theme()
	if draft.Upload {
		components.Text(c, "本地文件").FontSize(11)
		if len(draft.LocalPaths) == 0 {
			components.Text(c, "未选择本地文件").FontSize(11).TextColor(theme.TextMuted)
		}
		for _, path := range draft.LocalPaths {
			components.Text(c, path).FontSize(11).Grow(1).MinWidth(0)
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if components.Button(c, "选择文件…", components.ButtonOutline).Clicked() {
				vm.pickTransferFiles()
			}
			if components.Button(c, "选择文件夹…", components.ButtonOutline).Clicked() {
				vm.pickTransferFolder()
			}
		})
	} else {
		ui.Column(c).Gap(3).Children(func() {
			components.Text(c, "保存到").FontSize(11)
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				local := "请选择本地保存位置"
				if len(draft.LocalPaths) > 0 {
					local = draft.LocalPaths[0]
				}
				components.Text(c, local).FontSize(11).Grow(1).MinWidth(0)
				if components.Button(c, "选择位置…", components.ButtonOutline).Clicked() {
					vm.pickDownloadFolder()
				}
			})
		})
	}

	label := "上传到"
	if !draft.Upload {
		label = "设备上的文件或目录"
	}
	field(c, label, &draft.RemotePath, false)
	ui.Row(c).FillWidth().Children(func() {
		if components.Button(c, "浏览设备…", components.ButtonOutline).Clicked() {
			vm.browseTransferDevice()
		}
	})
	if draft.Err != "" {
		components.Text(c, draft.Err).FontSize(11)
	}
	ready := strings.TrimSpace(draft.RemotePath) != "" && len(draft.LocalPaths) > 0
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		if components.Button(c, "取消", components.ButtonOutline).Clicked() {
			vm.closeTransfer()
		}
		button := components.Button(c, "开始", components.ButtonPrimary)
		if !ready {
			button.Disabled(true)
			return
		}
		if button.Clicked() {
			vm.submitTransfer()
		}
	})
}

// transferResult draws the batch from the task list: progress while it runs,
// then what it ended as.
func (vm *ViewModel) transferResult(c *ui.Context, draft *transferDraft) {
	batch := draft.batch()
	tasks := vm.Manager.Tasks()
	view := batchSnapshot(batch, tasks, fileSize)
	switch view.State {
	case core.TaskSucceeded:
		components.Text(c, "已完成").FontSize(11)
	case core.TaskFailed:
		components.Text(c, "失败："+orPlaceholder(view.Reason, "任务失败")).FontSize(11)
	case core.TaskCancelled:
		components.Text(c, "已取消").FontSize(11)
	default:
		components.Text(c, batch.Title).FontSize(11)
		progressBlock(c, batch, tasks, fileSize)
	}
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		if view.State == core.TaskRunning {
			if components.Button(c, "取消", components.ButtonOutline).Clicked() {
				vm.cancelTransfer()
			}
			if components.Button(c, "关闭", components.ButtonOutline).Clicked() {
				vm.closeTransfer()
			}
			return
		}
		if components.Button(c, "关闭", components.ButtonPrimary).Clicked() {
			vm.closeTransfer()
		}
	})
}
