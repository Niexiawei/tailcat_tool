package app

import (
	"context"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// installUsersUnreadableNote is what the dialog says when the device would not
// list its profiles: the install still runs, exactly as it did before there was
// a choice, and the sentence is the only place that difference is visible.
const installUsersUnreadableNote = core.MsgUsersUnreadable + "，将安装到默认用户"

// installOverwriteNote spells out what -r means. "覆盖安装" is what a user calls
// it; keeping the data is the part they want to be sure of.
const installOverwriteNote = "覆盖安装：同包名的应用会被替换，应用数据保留。"

// installTimeout is how long one install may run. A large APK over a slow
// channel is minutes, not seconds.
const installTimeout = 10 * time.Minute

// installDraft is the 「安装 APK」 dialog: the file the user picked and which
// device user it goes to. A nil *installDraft closes the dialog.
type installDraft struct {
	Device uuid.UUID
	Path   string
	Users  []core.DeviceUser
	// UserID is the chosen install user; 0 is a legitimate id, so Chosen says
	// whether anything is selected at all. Without a user the command is
	// today's install -r.
	UserID int
	Chosen bool
	// Loading is true while the profile list is being read.
	Loading bool
	// Err is why the last step failed: the read, or the task start.
	Err string
	// Stage is the dialog's phase: form, then running. The result is read off
	// the task list, the way the transfer dialog does it.
	Stage   int
	TaskIDs []uuid.UUID
	Started time.Time
}

// installNote is the running stage's second line: which user the package goes
// to, when the dialog knew one.
func installNote(state *installDraft) string {
	if state == nil || !state.Chosen {
		return ""
	}
	for _, user := range state.Users {
		if user.ID == state.UserID {
			return "安装到用户 " + installUserLabel(user)
		}
	}
	return ""
}

// openInstallDialog shows the dialog — the file is chosen inside it — and reads
// the device's profiles in the background: a work profile can appear between
// connecting and installing.
func (vm *ViewModel) openInstallDialog(device uuid.UUID, path string) {
	state := &installDraft{Device: device, Path: path, Loading: true}
	vm.installMu.Lock()
	vm.APKInstall = state
	vm.installMu.Unlock()
	vm.requestFrame()
	go func() {
		users, err := vm.Manager.DeviceUsers(context.Background(), device)
		vm.installMu.Lock()
		if vm.APKInstall != state {
			// Closed while the read ran: nothing to fill in.
			vm.installMu.Unlock()
			return
		}
		state.Loading = false
		if err != nil {
			state.Err = core.MsgUsersUnreadable
			state.Users = nil
			state.Chosen = false
			vm.installMu.Unlock()
			vm.requestFrame()
			return
		}
		state.Users = users
		state.UserID, state.Chosen = defaultInstallUser(users)
		vm.installMu.Unlock()
		vm.requestFrame()
	}()
}

// defaultInstallUser picks the profile the device says is running now, and the
// first one when it says nothing.
func defaultInstallUser(users []core.DeviceUser) (int, bool) {
	if len(users) == 0 {
		return 0, false
	}
	for _, user := range users {
		if user.Current {
			return user.ID, true
		}
	}
	return users[0].ID, true
}

// installArgs is the adb command for a chosen user: the same -r as before, plus
// --user when the device named a user. keepUser is false when the list could
// not be read, and then the command is exactly today's.
func installArgs(path string, userID int, keepUser bool) []string {
	args := []string{"install", "-r"}
	if keepUser {
		args = append(args, "--user", itoa(userID))
	}
	return append(args, path)
}

// installUserLabel is what the picker shows: the number adb takes, then the
// profile's name, then a marker on the one that is running now.
func installUserLabel(user core.DeviceUser) string {
	label := itoa(user.ID) + " · " + user.Name
	if user.Current {
		label += "（当前）"
	}
	return label
}

// submitInstall starts the task and closes the dialog. The dialog stays open
// with a reason when the task could not be started at all; a task that fails
// later reports itself through the task list and the channel log, as every
// device operation does.
func (vm *ViewModel) submitInstall(state *installDraft) {
	if state == nil {
		return
	}
	if state.Path == "" {
		// The button is disabled without a file; this is the guard behind it,
		// so a keyboard path to it cannot start an install of nothing.
		vm.installMu.Lock()
		if vm.APKInstall == state {
			state.Err = "请选择要安装的 APK 文件"
		}
		vm.installMu.Unlock()
		vm.requestFrame()
		return
	}
	taskID, err := vm.Manager.BeginADBTask(context.Background(), state.Device,
		"安装 "+fileName(state.Path), installArgs(state.Path, state.UserID, state.Chosen), installTimeout)
	vm.installMu.Lock()
	if vm.APKInstall != state {
		vm.installMu.Unlock()
		return
	}
	if err != nil {
		state.Err = err.Error()
		vm.installMu.Unlock()
		vm.requestFrame()
		return
	}
	// The dialog stays: the install reports itself here, in the same window the
	// user pressed 安装 in.
	state.TaskIDs = []uuid.UUID{taskID}
	state.Started = time.Now()
	state.Stage = transferStageRunning
	vm.installMu.Unlock()
	vm.requestFrame()
}

// closeInstallDialog drops the dialog. Nothing to cancel: the install has not
// started until 安装 was pressed, and a started task keeps running in the task
// list.
func (vm *ViewModel) closeInstallDialog(state *installDraft) {
	vm.installMu.Lock()
	if vm.APKInstall == state {
		vm.APKInstall = nil
	}
	vm.installMu.Unlock()
}

// InstallView renders the install dialog: what is being installed, which user
// it goes to, and what that means.
func (vm *ViewModel) InstallView(c *ui.Context) {
	vm.installMu.Lock()
	state := vm.APKInstall
	var path, errText string
	var users []core.DeviceUser
	var loading bool
	if state != nil {
		path, errText, users, loading = state.Path, state.Err, state.Users, state.Loading
	}
	vm.installMu.Unlock()

	open := state != nil
	components.Dialog(c, &open, components.DialogOptions{Label: "安装 APK"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(480)
		panel.Children(func() {
			components.Text(c, "安装 APK").FontSize(18).Bold()
			if state != nil && state.Stage == transferStageRunning {
				vm.installRunning(c, state)
				return
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				components.Text(c, "APK 文件").FontSize(11).Width(70).Shrink(0)
				if path == "" {
					components.Text(c, "未选择 APK 文件").FontSize(11).TextColor(c.Theme().TextMuted)
					return
				}
				// The whole path, wrapped: the user is about to overwrite an
				// application and needs to see which build they picked.
				components.Text(c, path).FontSize(11).Grow(1).MinWidth(0)
			})
			// A row of its own so the button keeps its width: a Button directly
			// in the dialog column stretches into a full-width bar, which reads
			// as the primary action of this dialog.
			ui.Row(c).FillWidth().Children(func() {
				if !components.Button(c, "选择 APK 文件", components.ButtonOutline).Clicked() {
					return
				}
				if picked, ok := pickAPKFile("选择要安装的 APK 文件"); ok && picked != "" {
					vm.installMu.Lock()
					if vm.APKInstall == state {
						state.Path = picked
					}
					vm.installMu.Unlock()
					c.Invalidate()
				}
			})
			components.Text(c, installOverwriteNote).FontSize(11).TextColor(c.Theme().TextMuted)
			switch {
			case loading:
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					components.Spinner(c, components.SpinnerRing).Width(14).Height(14).Label("正在读取设备用户")
					components.Text(c, "正在读取设备用户…").FontSize(11)
				})
			case len(users) > 0:
				components.Text(c, "安装到用户").FontSize(11)
				sel := components.Select(c, &state.UserID)
				sel.Trigger.Width(220).Label("安装到用户")
				sel.Trigger.Children(func() {
					components.Text(c, installUserChoiceLabel(users, state.UserID)).SingleLine()
				})
				sel.Popup(func(ui.Element) {
					for _, user := range users {
						item := sel.Item(user.ID)
						item.FillWidth().Justify(ui.Start)
						item.Children(func() { components.Text(c, installUserLabel(user)).SingleLine() })
					}
				})
			}
			if errText != "" {
				note := "安装失败：" + errText
				if errText == core.MsgUsersUnreadable {
					// A missing list is not a failed install: say what will
					// happen instead of what went wrong.
					note = installUsersUnreadableNote
				}
				components.Text(c, note).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.closeInstallDialog(state)
					open = false
				}
				button := components.Button(c, "安装", components.ButtonPrimary)
				if loading || path == "" {
					// Nothing to install without a file, and the list is what
					// says which users there are.
					button.Disabled(true)
					return
				}
				if button.Clicked() {
					vm.submitInstall(state)
				}
			})
		})
	})
	if _, live := vm.installSnapshot(); !open && live {
		vm.closeInstallDialog(state)
	}
}

// installRunning draws the batch in the dialog that started it: the same bar
// the transfer dialog uses, so the two never disagree about progress.
func (vm *ViewModel) installRunning(c *ui.Context, state *installDraft) {
	batch := taskBatch{
		Device:  state.Device,
		Title:   "安装 " + fileName(state.Path),
		Note:    installNote(state),
		Kind:    taskInstall,
		Tasks:   state.TaskIDs,
		Started: state.Started,
	}
	tasks := vm.Manager.Tasks()
	view := batchSnapshot(batch, tasks, fileSize)
	components.Text(c, batch.Title).FontSize(11)
	if batch.Note != "" {
		components.Text(c, batch.Note).FontSize(11).TextColor(c.Theme().TextMuted)
	}
	switch view.State {
	case core.TaskSucceeded:
		components.Text(c, "已完成").FontSize(11)
	case core.TaskFailed:
		components.Text(c, "安装失败："+orPlaceholder(view.Reason, "任务失败")).FontSize(11)
	case core.TaskCancelled:
		components.Text(c, "已取消").FontSize(11)
	default:
		progressBlock(c, batch, tasks, fileSize)
	}
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		if view.State == core.TaskRunning {
			if components.Button(c, "取消", components.ButtonOutline).Clicked() {
				vm.cancelInstall(state)
			}
			if components.Button(c, "关闭", components.ButtonOutline).Clicked() {
				vm.closeInstallDialog(state)
			}
			return
		}
		if components.Button(c, "关闭", components.ButtonPrimary).Clicked() {
			vm.closeInstallDialog(state)
		}
	})
}

// cancelInstall stops the install and closes the dialog; closing without 取消
// leaves the task running, which the log page's task list shows.
func (vm *ViewModel) cancelInstall(state *installDraft) {
	vm.installMu.Lock()
	ids := append([]uuid.UUID(nil), state.TaskIDs...)
	if vm.APKInstall == state {
		vm.APKInstall = nil
	}
	vm.installMu.Unlock()
	for _, id := range ids {
		vm.Manager.CancelTask(id)
	}
	vm.requestFrame()
}

// installUserChoiceLabel is the picker's closed state: the label of the chosen
// user, or a prompt when nothing was chosen.
func installUserChoiceLabel(users []core.DeviceUser, chosen int) string {
	for _, user := range users {
		if user.ID == chosen {
			return installUserLabel(user)
		}
	}
	return "选择用户"
}

// installSnapshot is a copy of the dialog state, taken under the lock: the
// goroutine that reads the profile list keeps writing to the real one.
func (vm *ViewModel) installSnapshot() (installDraft, bool) {
	vm.installMu.Lock()
	defer vm.installMu.Unlock()
	if vm.APKInstall == nil {
		return installDraft{}, false
	}
	return *vm.APKInstall, true
}
