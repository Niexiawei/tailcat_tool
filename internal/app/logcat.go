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
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// logcatLevelTabs are the level floors the page offers: 全部 keeps every level,
// the rest keep that level and above, the way logcat's own spec reads.
var logcatLevelTabs = []struct {
	Label string
	Level core.LogcatLevel
}{
	{"全部", ""},
	{"调试", core.LogcatDebug},
	{"信息", core.LogcatInfo},
	{"警告", core.LogcatWarn},
	{"错误", core.LogcatError},
}

// logcatState is the one streaming log view: what the user chose, what the
// stream kept, and the table state that holds the column layout and the scroll.
//
// Every field is written on the render path only: the stream asks for a frame
// from its own goroutine and nothing else crosses over, so the page needs no
// lock of its own. The stream has one.
type logcatState struct {
	Device *uuid.UUID
	Filter core.LogcatFilter
	// Follow is the 跟随最新 switch; ListState.FollowEnd mirrors it.
	Follow bool
	// Paused freezes what the table shows while the stream keeps filling.
	Paused bool
	// stream is the running logcat, nil when there is none.
	stream *core.LogcatStream
	rows   []core.LogcatEntry
	// version and filterKey are what the rows were read from: a frame that
	// finds both unchanged does not copy the buffer again.
	version   uint64
	filterKey string
	received  uint64
	dropped   uint64
	exited    bool
	err       string
	// table keeps the column layout and the scroll between frames.
	table ui.ListState
}

// logcatState returns the page's state, creating it on first use. It lives on
// the view model, so leaving the page and coming back keeps the filters.
func (vm *ViewModel) logcatState() *logcatState {
	if vm.Logcat == nil {
		vm.Logcat = &logcatState{Follow: true}
	}
	return vm.Logcat
}

// onlineDevices lists the devices whose ADB session is up. A log stream needs a
// live serial, so a device that is merely saved is not on this list.
func (vm *ViewModel) onlineDevices() []core.Profile {
	var online []core.Profile
	for _, profile := range vm.Manager.AllProfiles() {
		session := vm.Manager.Session(profile.ID)
		if session.State == core.StateOnline && session.Serial != "" {
			online = append(online, profile)
		}
	}
	return online
}

// beginLogcatFrame marks the streaming page as not drawn yet; logcatPage sets it
// back while it builds, so a frame that did not draw it closes the stream.
func (vm *ViewModel) beginLogcatFrame() { vm.logcatDrawn = false }

// settleLogcat stops the stream when its page was not drawn this frame: a
// hidden page must not keep pulling lines off the device.
func (vm *ViewModel) settleLogcat() {
	if vm.logcatDrawn {
		return
	}
	if vm.Logcat != nil {
		vm.stopLogcatStream(vm.Logcat)
	}
}

// CloseLogcat stops the streaming page's logcat. The window calls it when it is
// closed: a hidden window draws no frames, so the page cannot notice that the
// user left and would keep reading the device channel.
func (vm *ViewModel) CloseLogcat() {
	if vm.Logcat != nil {
		vm.stopLogcatStream(vm.Logcat)
	}
}

// stopLogcatStream closes whatever is running for the page.
func (vm *ViewModel) stopLogcatStream(f *logcatState) {
	stream := f.stream
	if stream == nil {
		return
	}
	f.stream = nil
	if f.Device != nil {
		vm.Manager.StopLogcat(*f.Device)
		return
	}
	stream.Close()
}

// logcatPage is the Logcat page: the device, then the filters, then the stream.
func (vm *ViewModel) logcatPage(c *ui.Context) {
	vm.logcatDrawn = true
	f := vm.logcatState()
	vm.ensureLogcatDevice(f)
	vm.ensureLogcatStream(f)
	vm.refreshLogcatRows(f)
	// The follow switch owns the table's follow flag, and it is set here rather
	// than in logcatTable: an empty table is not built at all, and the switch
	// has to mean the same thing whether or not there is a row to scroll.
	f.table.FollowEnd = f.Follow
	online := vm.onlineDevices()
	theme := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(theme.Space(2)).Children(func() {
		if len(online) == 0 {
			placeholder(c, "没有已连接的设备，先到设备页连接一台。", 16)
			return
		}
		vm.logcatDeviceSelect(c, f, online)
		vm.logcatFiltersRow(c, f)
		vm.logcatBox(c, f)
		vm.logcatFootRow(c, f)
	})
}

// ensureLogcatDevice keeps the chosen device honest: a device that went away is
// dropped, and an empty choice falls back to the first connected one.
func (vm *ViewModel) ensureLogcatDevice(f *logcatState) {
	online := vm.onlineDevices()
	if f.Device != nil {
		for _, profile := range online {
			if profile.ID == *f.Device {
				return
			}
		}
	}
	if len(online) == 0 {
		f.Device = nil
		return
	}
	id := online[0].ID
	f.Device = &id
}

// ensureLogcatStream keeps one stream running for the chosen device.
func (vm *ViewModel) ensureLogcatStream(f *logcatState) {
	if f.Device == nil {
		vm.stopLogcatStream(f)
		return
	}
	if f.stream != nil && !f.stream.Closed() {
		return
	}
	// One attempt per user action: a start that failed would otherwise be
	// retried by every frame, spawning adb sixty times a second, and the
	// message on screen would never sit still long enough to read.
	if f.err != "" && f.stream == nil {
		return
	}
	f.stream = nil
	stream, err := vm.Manager.StartLogcat(context.Background(), *f.Device, core.LogcatOptions{OnLines: vm.requestFrame})
	if err != nil {
		f.err = err.Error()
		return
	}
	f.err = ""
	f.stream = stream
}

// refreshLogcatRows copies what the stream holds into the page's state. A
// paused page keeps the rows it shows: the point of pausing is that the reader
// does not move.
func (vm *ViewModel) refreshLogcatRows(f *logcatState) {
	if f.stream == nil {
		return
	}
	// A paused page keeps the rows it shows, but a page that was paused before
	// it ever read the buffer still has to read it once: an empty table under
	// 「已暂停」 looks like a page that lost its lines.
	if f.Paused && f.version != 0 {
		return
	}
	filterKey := string(f.Filter.MinLevel) + "\x00" + f.Filter.Tags + "\x00" + f.Filter.Terms
	if f.version != 0 && f.stream.Version() == f.version && f.filterKey == filterKey {
		return
	}
	snap := f.stream.Snapshot(f.Filter)
	f.rows = snap.Entries
	f.version = snap.Version
	f.filterKey = filterKey
	f.received = snap.Received
	f.dropped = snap.Dropped
	f.exited = snap.Exited
	f.err = snap.Err
}

// logcatDeviceSelect chooses which connected device to stream.
func (vm *ViewModel) logcatDeviceSelect(c *ui.Context, f *logcatState, online []core.Profile) {
	choice := ""
	if f.Device != nil {
		choice = f.Device.String()
	}
	sel := components.Select(c, &choice)
	sel.Trigger.FillWidth()
	sel.Trigger.Children(func() {
		components.Text(c, vm.logcatDeviceLabel(choice, online)).SingleLine()
	})
	sel.Popup(func(ui.Element) {
		for _, profile := range online {
			item := sel.Item(profile.ID.String())
			item.Children(func() {
				label := profile.Name + " · " + vm.Manager.Session(profile.ID).Serial
				components.Text(c, label).SingleLine()
			})
		}
	})
	if sel.Trigger.Changed() {
		if id, err := uuid.Parse(choice); err == nil && (f.Device == nil || *f.Device != id) {
			vm.switchLogcatDevice(f, id)
		}
	}
}

// switchLogcatDevice restarts the stream on another device and forgets what the
// previous one printed: two devices' lines in one table would be unreadable.
func (vm *ViewModel) switchLogcatDevice(f *logcatState, id uuid.UUID) {
	vm.stopLogcatStream(f)
	f.Device = &id
	f.rows = nil
	f.version = 0
	f.filterKey = ""
	f.err = ""
	f.exited = false
	f.received, f.dropped = 0, 0
}

// logcatDeviceLabel is what the picker's trigger shows.
func (vm *ViewModel) logcatDeviceLabel(choice string, online []core.Profile) string {
	id, err := uuid.Parse(choice)
	if err != nil {
		return "选择设备"
	}
	for _, profile := range online {
		if profile.ID == id {
			return profile.Name + " · " + vm.Manager.Session(id).Serial
		}
	}
	return "选择设备"
}

// logcatFiltersRow is the level tabs with the two text boxes, the follow switch
// and the pause button.
func (vm *ViewModel) logcatFiltersRow(c *ui.Context, f *logcatState) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Shrink(0).Gap(theme.Space(1.5)).Children(func() {
		ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			// The group carries its name: five segments starting with 全部
			// would otherwise be a guess.
			components.Text(c, "级别").FontSize(11).TextColor(theme.TextMuted).Shrink(0)
			// The chosen value is read back from the widget rather than from
			// Changed(): a toggle can land a frame before the flag does.
			level := logcatLevelIndex(f.Filter.MinLevel)
			components.Tabs(c, &level, components.TabsSegmented, "全部", "调试", "信息", "警告", "错误")
			if picked := logcatLevelAt(level); picked != f.Filter.MinLevel {
				f.Filter.MinLevel = picked
			}
			ui.Row(c).Grow(1).Justify(ui.End).AlignItems(ui.Center).Gap(theme.Space(2)).Children(func() {
				follow := f.Follow
				components.Switch(c, &follow, "跟随最新")
				if follow != f.Follow {
					f.Follow = follow
				}
				vm.logcatPauseButton(c, f)
			})
		})
		ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			components.TextBox(c, &f.Filter.Tags, components.TextBoxOptions{}).
				FillWidth().Grow(1).Placeholder("标签筛选：如 ActivityManager，空格分隔多个")
			components.TextBox(c, &f.Filter.Terms, components.TextBoxOptions{}).
				FillWidth().Grow(1).Placeholder("内容筛选：空格分隔的多个关键词都要匹配")
		})
	})
}

// logcatPauseButton freezes and resumes what the table shows.
func (vm *ViewModel) logcatPauseButton(c *ui.Context, f *logcatState) {
	label, icon := "暂停", lucide.Pause
	if f.Paused {
		label, icon = "继续", lucide.Play
	}
	theme := c.Theme()
	size := max(float32(12), theme.Rem(.875))
	button := components.Button(c, "", components.ButtonSecondary).Label(label)
	button.Gap(theme.Space(1)).Shrink(0).Children(func() {
		components.Icon(c, icon).Size(size, size)
		components.Text(c, label).SingleLine()
	})
	if button.Clicked() {
		f.Paused = !f.Paused
	}
}

// logcatBox is the rows, inside a box the page keeps for them whether or not
// there is anything to show.
func (vm *ViewModel) logcatBox(c *ui.Context, f *logcatState) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		switch {
		case len(f.rows) > 0:
			vm.logcatTable(c, f)
		case f.stream == nil && f.err != "":
			placeholder(c, "日志流没有启动", 16)
		case f.received > 0 && !f.Paused && logcatFilterActive(f.Filter):
			placeholder(c, "没有符合筛选条件的日志", 16)
		case f.exited:
			placeholder(c, "日志流已结束，没有可显示的行", 16)
		default:
			placeholder(c, "正在接收日志…", 16)
		}
	})
}

// logcatColumns is the table's column layout: the timestamp and the tag need
// the most room, the level and the pid the least. The message takes whatever is
// left and the copy button closes the row.
func logcatColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "at", Title: "时间", Width: 150},
		{ID: "level", Title: "级别", Width: 64},
		{ID: "pid", Title: "PID", Width: 64},
		{ID: "tag", Title: "标签", Width: 160},
		{ID: "text", Title: "内容"},
		{ID: "copy", Title: "", Width: 44},
	}
}

// logcatTable fills the box with the rows, oldest first, so following the end
// is following the newest line.
func (vm *ViewModel) logcatTable(c *ui.Context, f *logcatState) {
	rows := f.rows
	state := &f.table
	// Seq is the row's identity: two log lines can be identical, and a table
	// keyed on the text would lose its place on every burst.
	state.Key = func(row int) any { return rows[row].Seq }
	tableCard(c, state, logcatColumns(), len(rows), tableOptions{
		Label: "Logcat 表", Grow: true,
	}, func(row, col int) {
		entry := rows[row]
		if col == len(logcatColumns())-1 {
			if components.IconButton(c, lucide.Copy(), "复制这一行", components.IconButtonCompact).Clicked() {
				vm.copyLogcatLine(entry)
			}
			return
		}
		ui.Row(c).FillWidth().MinWidth(0).Children(func() {
			switch col {
			case 0:
				components.Text(c, entry.At.Format("01-02 15:04:05.000")).FontSize(11).SingleLine()
			case 1:
				components.Badge(c, entry.Level.Label(), components.BadgeOptions{Tone: logcatTone(entry.Level)})
			case 2:
				components.Text(c, itoa(entry.PID)).FontSize(11).SingleLine()
			case 3:
				components.Tooltip(c, components.Text(c, entry.Tag).FontSize(11).SingleLine(), entry.Tag)
			default:
				// The message wraps to as many lines as it needs: a stack trace
				// read one truncated line at a time is useless, and a tooltip is
				// not how you read a crash.
				components.Text(c, entry.Text).FontSize(11)
			}
		})
	})
}

// copyLogcatLine puts one entry on the clipboard in the same shape the export
// writes, so a line can be pasted into a bug report as it stands.
func (vm *ViewModel) copyLogcatLine(entry core.LogcatEntry) {
	if err := writeClipboard(logcatLineText(entry)); err != nil {
		vm.FailText("复制失败：" + err.Error())
		return
	}
	vm.Notify("已复制这一行")
}

// logcatLineText is one entry as text: the export's line and the clipboard's.
func logcatLineText(entry core.LogcatEntry) string {
	return entry.At.Format("2006-01-02 15:04:05.000") + " " +
		string(entry.Level) + "/" + entry.Tag + "(" + itoa(entry.PID) + "): " + entry.Text
}

// logcatTone is the badge colour: errors and fatals red, warnings yellow.
func logcatTone(level core.LogcatLevel) components.BadgeTone {
	switch level {
	case core.LogcatError, core.LogcatFatal:
		return components.BadgeDanger
	case core.LogcatWarn:
		return components.BadgeWarning
	default:
		return components.BadgeNeutral
	}
}

// logcatFootRow says what the stream is doing and offers what can be done with
// what it kept. It is drawn even when the box is empty, so the actions keep
// their place.
func (vm *ViewModel) logcatFootRow(c *ui.Context, f *logcatState) {
	theme := c.Theme()
	ui.Row(c).FillWidth().Shrink(0).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(theme.Space(2)).Children(func() {
		components.Text(c, logcatStatusText(f)).FontSize(11).TextColor(theme.TextMuted)
		ui.Row(c).Shrink(0).Gap(theme.Space(1.5)).AlignItems(ui.Center).Children(func() {
			if components.Button(c, "导出…", components.ButtonOutline).Height(26).Clicked() {
				vm.exportLogcat(f)
			}
			if components.Button(c, "清空缓冲", components.ButtonOutline).Height(26).Clicked() {
				vm.clearLogcat(f)
			}
			if (f.exited || (f.stream == nil && f.err != "")) &&
				components.Button(c, "重新开始", components.ButtonPrimary).Height(26).Clicked() {
				vm.restartLogcat(f)
			}
		})
	})
}

// clearLogcat drops the kept lines. The counters keep counting: "this stream
// received N lines" stays true after the reader emptied it.
func (vm *ViewModel) clearLogcat(f *logcatState) {
	f.rows = nil
	f.version = 0
	if f.stream != nil {
		f.stream.Clear()
	}
}

// restartLogcat starts a fresh stream after the last one ended. It is a user
// action on purpose: a device that went away must not be re-read behind the
// user's back.
func (vm *ViewModel) restartLogcat(f *logcatState) {
	vm.stopLogcatStream(f)
	f.rows = nil
	f.version = 0
	f.filterKey = ""
	f.exited = false
	f.err = ""
	f.received, f.dropped = 0, 0
}

// exportLogcat asks where to save and writes the rows the page shows: the
// filter is the point of the page, so the file holds what the user is looking
// at, not the whole buffer.
func (vm *ViewModel) exportLogcat(f *logcatState) {
	if len(f.rows) == 0 {
		vm.FailText("没有可导出的行")
		return
	}
	target, err := saveLocalPath(mygo.SaveDialogOptions{
		Title:       "导出 Logcat",
		DefaultPath: "logcat-" + time.Now().Format("20060102-150405") + ".txt",
		Filters:     []mygo.FileFilter{{Name: "文本文件", Extensions: []string{"txt"}}},
	})
	if err != nil || target == "" {
		// Cancelling a save dialog is not an error, and the system one reports
		// a dismissal as an error on some platforms.
		return
	}
	rows := append([]core.LogcatEntry(nil), f.rows...)
	text := logcatExportText(rows)
	go func() {
		if err := os.WriteFile(target, []byte(text), 0o600); err != nil {
			vm.Fail(core.Errorf("无法写入文件：%v", err))
			return
		}
		vm.Notify("已导出 " + itoa(len(rows)) + " 行到 " + target)
	}()
}

// logcatExportText is the file's content: one line per entry, oldest first, in
// the shape a person reads in a text editor. The file carries the full date
// even though the table shows only month and day: a file outlives the day it
// was written.
func logcatExportText(rows []core.LogcatEntry) string {
	var b strings.Builder
	for _, entry := range rows {
		b.WriteString(logcatLineText(entry))
		b.WriteString("\n")
	}
	return b.String()
}

// logcatFilterActive reports whether the user narrowed what the page shows.
func logcatFilterActive(filter core.LogcatFilter) bool {
	return filter.MinLevel != "" || strings.TrimSpace(filter.Tags) != "" || strings.TrimSpace(filter.Terms) != ""
}

// logcatStatusText is the foot row's sentence; tests assert it verbatim.
func logcatStatusText(f *logcatState) string {
	switch {
	case f.err != "" && f.exited:
		return "日志流已结束：" + f.err
	case f.exited:
		return "日志流已结束"
	case f.err != "":
		return "日志流无法启动：" + f.err
	}
	// 行 is what the device printed, 条 is what the page shows: one call can
	// print several lines and they are one entry.
	text := "已接收 " + itoa(int(f.received)) + " 行 · 显示 " + itoa(len(f.rows)) + " 条"
	if f.dropped > 0 {
		// The limit belongs next to the number: "丢弃 29800 条" alone reads as
		// data loss, and the user has to ask what the buffer even holds.
		text += " · 缓冲已满，丢弃 " + itoa(int(f.dropped)) + " 条（上限 " + logcatBufferLimit() + "）"
	}
	if f.Paused {
		text += " · 已暂停"
	}
	return text
}

// logcatBufferLimit is what the foot row says the ring holds: both bounds,
// because either one can be the one that evicted.
func logcatBufferLimit() string {
	return itoa(core.LogcatMaxLines) + " 条 / " + itoa(core.LogcatMaxBytes>>20) + " MB"
}

// logcatLevelIndex and logcatLevelAt convert between the filter's floor and the
// tab the user sees.
func logcatLevelIndex(level core.LogcatLevel) int {
	for i, tab := range logcatLevelTabs {
		if tab.Level == level {
			return i
		}
	}
	return 0
}

// logcatLevelAt is the inverse of logcatLevelIndex.
func logcatLevelAt(index int) core.LogcatLevel {
	if index < 0 || index >= len(logcatLevelTabs) {
		return ""
	}
	return logcatLevelTabs[index].Level
}

// placeholder centres a message in the box the page keeps for its content.
func placeholder(c *ui.Context, text string, size float32) {
	ui.Column(c).Fill().Center().Children(func() {
		components.Text(c, text).FontSize(size).TextColor(c.Theme().TextMuted)
	})
}
