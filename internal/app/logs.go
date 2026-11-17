package app

import (
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// runningTasks keeps the tasks of one device that are still running.
func runningTasks(tasks []core.Task, id uuid.UUID) []core.Task {
	running := make([]core.Task, 0, 2)
	for _, task := range tasks {
		if task.DeviceID == id && task.State == core.TaskRunning {
			running = append(running, task)
		}
	}
	return running
}

// logPage is the device page's 日志 tab. It is a thin wrapper around the one
// log UI: the device is fixed to this device, the picker is hidden, and the
// tab keeps following the newest line the way the old text view did.
func (vm *ViewModel) logPage(c *ui.Context, profile core.Profile) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
			components.Text(c, "通道日志与任务").FontSize(13).Bold()
			if running := runningTasks(vm.Manager.Tasks(), profile.ID); len(running) > 0 {
				components.Text(c, "运行中 "+itoa(len(running))+" 个任务").FontSize(11)
				if components.Button(c, "取消全部", components.ButtonOutline).Clicked() {
					for _, task := range running {
						vm.Manager.CancelTask(task.ID)
					}
				}
			}
		})
		vm.logView(c, vm.logFilterFor(profile.ID), logViewOptions{AllowClear: true})
	})
}

// logFilterFor is one device's log view. It starts filtered to that device and
// following the newest line, because a tab under a device is read while
// watching it work.
func (vm *ViewModel) logFilterFor(id uuid.UUID) *logFilter {
	vm.logMu.Lock()
	defer vm.logMu.Unlock()
	if filter, ok := vm.logFilters[id]; ok {
		return filter
	}
	device := id
	filter := &logFilter{Device: &device, Range: logRangeToday, Follow: true, selected: -1}
	if vm.logFilters == nil {
		vm.logFilters = map[uuid.UUID]*logFilter{}
	}
	vm.logFilters[id] = filter
	return filter
}

// The log page's time ranges, in the order its tabs show them.
const (
	logRangeToday = iota
	logRangeWeek
	logRangeMonth
	logRangeAll
)

// logRangeLabels is what the range tabs say; the report at the bottom of the
// page repeats the chosen one, so one list keeps them in step.
var logRangeLabels = []string{"今日", "7 天", "30 天", "全部"}

// logFollowInterval is how often a following page asks for new rows. The query
// is incremental (Since = the last row), so this stays cheap on a large file.
const logFollowInterval = 2 * time.Second

// logFilter is one log view: what the user chose, what the reader answered, and
// the table state that keeps the column layout and the scroll position.
type logFilter struct {
	Device *uuid.UUID
	Level  core.LogLevel
	Range  int
	Search string
	Follow bool

	// logMu guards what a query goroutine writes: entries, query, loading and
	// the bookkeeping around them.
	entries   []core.TimelineEntry
	query     core.LogQuery
	loaded    bool
	loading   bool
	tooMany   bool
	err       string
	lastAt    time.Time
	following bool
	// jumpToTop asks the next frame to scroll back to the newest row after
	// rows were prepended; the table state belongs to the render thread.
	jumpToTop bool

	table components.TableVirtualizedState
	// selected is the chosen row; the table writes it and OnChange reads it.
	selected int
}

// logViewOptions says how one caller of logView differs from the other: the
// page shows a device picker, the device tab has its device fixed.
type logViewOptions struct {
	ShowDevicePicker bool
	AllowClear       bool
}

// since turns the chosen range into a lower bound for the query. The bound is
// truncated to a day on purpose: a moving "now − 7 days" would differ on every
// frame, the page would never recognise its own query, and it would re-read the
// whole range sixty times a second.
func (f *logFilter) since() time.Time {
	now := time.Now()
	day := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	switch f.Range {
	case logRangeToday:
		return day(now)
	case logRangeWeek:
		return day(now.AddDate(0, 0, -7))
	case logRangeMonth:
		return day(now.AddDate(0, 0, -30))
	default:
		return time.Time{}
	}
}

// buildQuery is the reader call this filter asks for; the level and the
// keywords are not part of the lower bound, so switching them re-reads.
func (f *logFilter) buildQuery() core.LogQuery {
	return core.LogQuery{
		DeviceID: f.Device,
		Level:    f.Level,
		Since:    f.since(),
		Search:   f.Search,
		Limit:    core.DefaultLogLimit,
	}
}

// sameLogQuery reports whether the shown rows already answer query.
func sameLogQuery(shown, query core.LogQuery) bool {
	if (shown.DeviceID == nil) != (query.DeviceID == nil) {
		return false
	}
	if shown.DeviceID != nil && *shown.DeviceID != *query.DeviceID {
		return false
	}
	return shown.Level == query.Level && shown.Since.Equal(query.Since) &&
		shown.Search == query.Search && shown.Limit == query.Limit
}

// logView is the only log UI. The page and the device tab differ only in the
// filter they pass and whether the device picker is shown.
func (vm *ViewModel) logView(c *ui.Context, f *logFilter, opts logViewOptions) {
	vm.markLogVisible(f)
	vm.ensureLogQuery(f)
	vm.ensureLogFollow(f)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(8).Children(func() {
		vm.logFiltersRow(c, f, opts)
		vm.logTimeline(c, f)
		vm.logFootRow(c, f, opts)
	})
}

// logFiltersRow is the four control rows: device, level, range and the search
// plus follow switch.
func (vm *ViewModel) logFiltersRow(c *ui.Context, f *logFilter, opts logViewOptions) {
	ui.Column(c).FillWidth().Shrink(0).Gap(6).Children(func() {
		if opts.ShowDevicePicker {
			vm.logDeviceSelect(c, f)
		}
		level := logLevelIndex(f.Level)
		if components.Tabs(c, &level, components.TabsSegmented, "全部", "错误", "警告", "信息").List.Changed() {
			f.Level = logLevelAt(level)
		}
		rng := f.Range
		if components.Tabs(c, &rng, components.TabsSegmented, logRangeLabels...).List.Changed() {
			f.Range = rng
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			components.TextBox(c, &f.Search, components.TextBoxOptions{}).
				FillWidth().Grow(1).Placeholder("搜索日志：空格分隔的多个关键词都要匹配")
			// The follow goroutine reads the switch under logMu, so the
			// widget writes a local copy and the filter takes it under the lock.
			follow := f.Follow
			if components.Switch(c, &follow, "跟随最新").Changed() {
				vm.logMu.Lock()
				f.Follow = follow
				vm.logMu.Unlock()
				vm.ensureLogFollow(f)
			}
			if components.Button(c, "刷新", components.ButtonGhost).Clicked() {
				vm.refreshLogs(f)
			}
		})
	})
}

// logDeviceSelect chooses between every device, the app's own lines, or one
// device.
func (vm *ViewModel) logDeviceSelect(c *ui.Context, f *logFilter) {
	choice := deviceChoiceKey(f.Device)
	sel := components.Select(c, &choice)
	sel.Trigger.FillWidth()
	sel.Trigger.Children(func() { components.Text(c, vm.deviceChoiceLabel(choice)).SingleLine() })
	sel.Popup(func(ui.Element) {
		for _, option := range vm.deviceChoiceKeys() {
			item := sel.Item(option)
			item.Children(func() { components.Text(c, vm.deviceChoiceLabel(option)).SingleLine() })
		}
	})
	if sel.Trigger.Changed() {
		f.Device = deviceChoiceValue(choice)
	}
}

// deviceChoiceKey turns a filter into the select's option key.
func deviceChoiceKey(device *uuid.UUID) string {
	switch {
	case device == nil:
		return logChoiceAll
	case *device == uuid.Nil:
		return logChoiceApp
	default:
		return device.String()
	}
}

// deviceChoiceValue is the inverse of deviceChoiceKey.
func deviceChoiceValue(key string) *uuid.UUID {
	switch key {
	case logChoiceAll:
		return nil
	case logChoiceApp:
		none := uuid.Nil
		return &none
	default:
		id, err := uuid.Parse(key)
		if err != nil {
			return nil
		}
		return &id
	}
}

// The two fixed options of the device select; a device's key is its id.
const (
	logChoiceAll = "all"
	logChoiceApp = "app"
)

func (vm *ViewModel) deviceChoiceKeys() []string {
	keys := []string{logChoiceAll, logChoiceApp}
	for _, profile := range vm.Manager.AllProfiles() {
		keys = append(keys, profile.ID.String())
	}
	return keys
}

func (vm *ViewModel) deviceChoiceLabel(key string) string {
	switch key {
	case logChoiceAll:
		return "全部设备与应用"
	case logChoiceApp:
		return "只看应用"
	}
	if id, err := uuid.Parse(key); err == nil {
		for _, profile := range vm.Manager.AllProfiles() {
			if profile.ID == id {
				return profile.Name
			}
		}
	}
	return "全部设备与应用"
}

// logRowKey identifies a row across re-queries, so selection follows its row
// rather than its index when newer rows arrive at the top.
type logRowKey struct {
	At     int64
	Source core.LogSource
	TaskID uuid.UUID
	Text   string
}

// logColumns is the timeline's column layout.
func logColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "at", Title: "时间", Width: 150},
		{ID: "level", Title: "级别", Width: 76},
		{ID: "source", Title: "来源", Width: 76},
		{ID: "device", Title: "设备", Width: 150},
		{ID: "text", Title: "内容"},
	}
}

// logTimeline is the merged rows, newest first.
func (vm *ViewModel) logTimeline(c *ui.Context, f *logFilter) {
	entries, loaded, loading, _, errText := vm.logSnapshot(f)
	if errText != "" {
		components.Text(c, "日志读取失败："+errText).FontSize(11)
		return
	}
	if len(entries) == 0 {
		note := "正在读取日志…"
		if loaded && !loading {
			note = "这段时间没有日志"
		}
		components.Text(c, note).FontSize(11)
		return
	}
	state := &f.table
	vm.logMu.Lock()
	jump := f.jumpToTop
	f.jumpToTop = false
	vm.logMu.Unlock()
	if jump {
		state.List.ScrollTo(0, ui.Start)
	}
	state.List.Selected = &f.selected
	state.List.OnChange = func() {
		row := f.selected
		if row < 0 || row >= len(entries) {
			return
		}
		// A task row is the only row that opens something; a log line has
		// nothing behind it.
		if entry := entries[row]; entry.Source == core.LogTask {
			vm.openLogOutput(entry)
		}
	}
	state.List.Key = func(row int) any {
		entry := entries[row]
		return logRowKey{At: entry.At.UnixNano(), Source: entry.Source, TaskID: entry.TaskID, Text: entry.Text}
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		components.TableVirtualized(c, state, logColumns(), len(entries), func(row, col int) {
			entry := entries[row]
			switch col {
			case 0:
				components.Text(c, entry.At.Format("2006-01-02 15:04:05")).FontSize(11).SingleLine()
			case 1:
				components.Badge(c, entry.Level.Label(), components.BadgeOptions{Tone: logLevelTone(entry.Level)})
			case 2:
				components.Text(c, entry.Source.Label()).FontSize(11).SingleLine()
			case 3:
				components.Text(c, logDeviceCell(entry)).FontSize(11).SingleLine()
			default:
				components.Tooltip(c, components.Text(c, entry.Text).FontSize(11).SingleLine(), entry.Text)
			}
		})
	})
}

// logDeviceCell says which device a row belongs to; an app line belongs to
// nobody.
func logDeviceCell(entry core.TimelineEntry) string {
	if entry.DeviceName != "" {
		return entry.DeviceName
	}
	if entry.DeviceID == uuid.Nil {
		return "—"
	}
	return entry.DeviceID.String()
}

// logFootRow reports a truncated timeline below the table.
func (vm *ViewModel) logFootRow(c *ui.Context, f *logFilter, opts logViewOptions) {
	_, _, _, tooMany, _ := vm.logSnapshot(f)
	if !tooMany && !opts.AllowClear {
		return
	}
	ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
		if tooMany {
			components.Text(c, "只显示最近 "+itoa(core.DefaultLogLimit)+" 条，请缩小时间范围").FontSize(11)
		}
		ui.Row(c).Grow(1).Children(func() {})
		if opts.AllowClear {
			if components.Button(c, "清空日志…", components.ButtonGhost).Clicked() {
				vm.openLogClear(f)
			}
		}
	})
}

// logSnapshot takes everything the render path needs in one lock.
func (vm *ViewModel) logSnapshot(f *logFilter) (entries []core.TimelineEntry, loaded, loading, tooMany bool, errText string) {
	vm.logMu.Lock()
	defer vm.logMu.Unlock()
	return append([]core.TimelineEntry(nil), f.entries...), f.loaded, f.loading, f.tooMany, f.err
}

// ensureLogQuery asks the reader when the filter no longer matches what is
// shown. It runs on the render path, so the read happens in a goroutine and
// asks for one more frame when it lands.
func (vm *ViewModel) ensureLogQuery(f *logFilter) {
	if vm.History == nil {
		return
	}
	query := f.buildQuery()
	vm.logMu.Lock()
	if f.loading || (f.loaded && sameLogQuery(f.query, query)) {
		vm.logMu.Unlock()
		return
	}
	f.loading = true
	vm.logMu.Unlock()
	go vm.runLogQuery(f, query)
}

// refreshLogs re-reads the range the filter describes, keeping what is shown
// until the answer lands.
func (vm *ViewModel) refreshLogs(f *logFilter) {
	if vm.History == nil {
		return
	}
	query := f.buildQuery()
	vm.logMu.Lock()
	if f.loading {
		vm.logMu.Unlock()
		return
	}
	f.loading = true
	vm.logMu.Unlock()
	go vm.runLogQuery(f, query)
}

// runLogQuery reads one whole range and replaces what the page shows.
func (vm *ViewModel) runLogQuery(f *logFilter, query core.LogQuery) {
	entries, err := vm.History.Timeline(query)
	vm.logMu.Lock()
	f.loading = false
	f.loaded = true
	f.query = query
	f.entries = entries
	f.tooMany = len(entries) >= query.Limit
	f.err = ""
	f.lastAt = time.Time{}
	if len(entries) > 0 {
		f.lastAt = entries[0].At
	}
	if err != nil {
		f.entries = nil
		f.err = err.Error()
	}
	vm.logMu.Unlock()
	vm.requestFrame()
}

// followLogs asks only for rows newer than the newest one shown and puts them
// on top: the timeline is newest first, so the reader stays where it is.
func (vm *ViewModel) followLogs(f *logFilter) {
	if vm.History == nil {
		return
	}
	vm.logMu.Lock()
	if !f.loaded || f.lastAt.IsZero() {
		vm.logMu.Unlock()
		return
	}
	query := f.query
	query.Since = f.lastAt
	vm.logMu.Unlock()

	entries, err := vm.History.Timeline(query)
	if err != nil || len(entries) == 0 {
		return
	}
	vm.logMu.Lock()
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.At.Before(f.lastAt) || f.hasLocked(entry) {
			continue
		}
		f.entries = append([]core.TimelineEntry{entry}, f.entries...)
	}
	if newest := entries[0].At; newest.After(f.lastAt) {
		f.lastAt = newest
	}
	// New rows push the old ones down; a following page shows the newest.
	f.jumpToTop = true
	vm.logMu.Unlock()
	vm.requestFrame()
}

// hasLocked reports whether entry is already in the timeline.
func (f *logFilter) hasLocked(entry core.TimelineEntry) bool {
	for _, shown := range f.entries {
		if shown.At.Equal(entry.At) && shown.Source == entry.Source &&
			shown.TaskID == entry.TaskID && shown.Text == entry.Text {
			return true
		}
	}
	return false
}

// ensureLogFollow keeps one wake-up goroutine per view: it asks for new rows
// every couple of seconds while the switch is on, and exits when it is off.
func (vm *ViewModel) ensureLogFollow(f *logFilter) {
	vm.logMu.Lock()
	on := f.Follow
	defer vm.logMu.Unlock()
	if !on || f.following {
		f.following = f.following && on
		return
	}
	f.following = true
	go func() {
		for {
			time.Sleep(logFollowInterval)
			if !vm.logFollowWanted(f) {
				vm.logMu.Lock()
				f.following = false
				vm.logMu.Unlock()
				return
			}
			vm.followLogs(f)
		}
	}()
}

// logFollowWanted reports whether a wake-up is still useful: the switch is on
// and this view is the one being drawn. A hidden view must not keep reading the
// database and asking for frames nobody sees.
func (vm *ViewModel) logFollowWanted(f *logFilter) bool {
	vm.logMu.Lock()
	defer vm.logMu.Unlock()
	return f.following && f.Follow && vm.activeLog == f
}

// clearActiveLog forgets which log view drew last. It runs once per frame
// before the pages; a log view that is actually drawn sets it again.
func (vm *ViewModel) clearActiveLog() {
	vm.logMu.Lock()
	vm.activeLog = nil
	vm.logMu.Unlock()
}

// markLogVisible remembers which view is drawing and stops the previous one
// from following: a hidden page must not keep querying the database.
func (vm *ViewModel) markLogVisible(f *logFilter) {
	vm.logMu.Lock()
	previous := vm.activeLog
	vm.activeLog = f
	if previous != nil && previous != f {
		previous.following = false
	}
	vm.logMu.Unlock()
}

// requestFrame asks for one more paint; nil during tests that drive frames.
func (vm *ViewModel) requestFrame() {
	if vm.Invalidate != nil {
		vm.Invalidate()
	}
}

// logLevelIndex and logLevelAt convert between the filter's level and the tab
// the user sees.
func logLevelIndex(level core.LogLevel) int {
	switch level {
	case core.LogError:
		return 1
	case core.LogWarn:
		return 2
	case core.LogInfo:
		return 3
	default:
		return 0
	}
}

func logLevelAt(index int) core.LogLevel {
	switch index {
	case 1:
		return core.LogError
	case 2:
		return core.LogWarn
	case 3:
		return core.LogInfo
	default:
		return ""
	}
}

// logLevelTone is the badge colour: errors red, warnings yellow, info grey.
func logLevelTone(level core.LogLevel) components.BadgeTone {
	switch level {
	case core.LogError:
		return components.BadgeDanger
	case core.LogWarn:
		return components.BadgeWarning
	default:
		return components.BadgeNeutral
	}
}

// pageLogFilter is the log page's own filter: every device, today, not
// following, because a page you open to read should not move under you.
func (vm *ViewModel) pageLogFilter() *logFilter {
	if vm.PageLogFilter == nil {
		// selected starts at -1 so the first row is not already chosen: a
		// click on it must still report the change.
		vm.PageLogFilter = &logFilter{Range: logRangeToday, selected: -1}
	}
	return vm.PageLogFilter
}

// logsPage is the standalone log page.
func (vm *ViewModel) logsPage(c *ui.Context) {
	ui.Column(c).Fill().Padding(20).Gap(10).Children(func() {
		components.Text(c, "全部日志").FontSize(18).Bold()
		vm.logView(c, vm.pageLogFilter(), logViewOptions{ShowDevicePicker: true, AllowClear: true})
	})
}

// logOutput is the dialog showing what one task printed.
type logOutput struct {
	TaskID  uuid.UUID
	Text    string
	Loading bool
	Err     string
}

// logClear is the confirmation dialog for deleting a range of log lines.
type logClear struct {
	Count  int
	Range  int
	Device *uuid.UUID
	Busy   bool
	Error  string
	// filter is the view the dialog was opened from, so the refresh after a
	// clear re-reads what the user is looking at.
	filter *logFilter
}

// openLogOutput reads a task's output off the render path and shows it.
func (vm *ViewModel) openLogOutput(entry core.TimelineEntry) {
	if vm.History == nil || entry.TaskID == uuid.Nil {
		return
	}
	vm.logMu.Lock()
	vm.LogOutput = &logOutput{TaskID: entry.TaskID, Loading: true}
	vm.logMu.Unlock()
	vm.requestFrame()
	go func() {
		text, err := vm.History.TaskOutput(entry.TaskID)
		vm.logMu.Lock()
		if output := vm.LogOutput; output != nil && output.TaskID == entry.TaskID {
			output.Loading = false
			output.Text = text
			if err != nil {
				output.Err = err.Error()
			}
		}
		vm.logMu.Unlock()
		vm.requestFrame()
	}()
}

// LogOutputView renders the task output dialog.
func (vm *ViewModel) LogOutputView(c *ui.Context) {
	vm.logMu.Lock()
	output := vm.LogOutput
	var text, errText string
	var loading bool
	if output != nil {
		text, errText, loading = output.Text, output.Err, output.Loading
	}
	vm.logMu.Unlock()

	open := output != nil
	components.Dialog(c, &open, components.DialogOptions{Label: "任务输出"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(760)
		panel.Children(func() {
			components.Text(c, "任务输出").FontSize(18).Bold()
			switch {
			case loading:
				components.Spinner(c, components.SpinnerRing).Label("正在读取任务输出")
			case errText != "":
				components.Text(c, errText).FontSize(11)
			case text == "":
				components.Text(c, "这个任务没有输出").FontSize(11)
			default:
				// Scrolled text rather than a text box: the output is long and
				// read-only, and a label keeps it selectable through the
				// dialog's own text handling.
				ui.Scroll(c).FillWidth().Height(360).Children(func() {
					components.Text(c, text).FontSize(11).Font("monospace")
				})
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "关闭", components.ButtonSecondary).Clicked() {
					vm.logMu.Lock()
					vm.LogOutput = nil
					vm.logMu.Unlock()
					open = false
				}
			})
		})
	})
	if !open {
		vm.logMu.Lock()
		vm.LogOutput = nil
		vm.logMu.Unlock()
	}
}

// openLogClear asks before deleting a range, and says what the range is.
func (vm *ViewModel) openLogClear(f *logFilter) {
	entries, _, _, _, _ := vm.logSnapshot(f)
	vm.logMu.Lock()
	vm.LogClear = &logClear{Count: len(entries), Range: f.Range, Device: f.Device, filter: f}
	vm.logMu.Unlock()
	vm.requestFrame()
}

// LogClearView renders the clear confirmation and runs the deletion.
func (vm *ViewModel) LogClearView(c *ui.Context) {
	vm.logMu.Lock()
	dialog := vm.LogClear
	var count, rng int
	var busy bool
	var errText string
	if dialog != nil {
		count, rng, busy, errText = dialog.Count, dialog.Range, dialog.Busy, dialog.Error
	}
	vm.logMu.Unlock()

	open := dialog != nil
	components.Dialog(c, &open, components.DialogOptions{Label: "清空日志"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(520)
		panel.Children(func() {
			components.Text(c, "清空日志").FontSize(18).Bold()
			components.Text(c, "清空日志（应用与通道日志），共 "+itoa(count)+" 条；任务记录不会被删除。").FontSize(11)
			components.Text(c, "删除范围是当前的时间段与设备筛选（"+logRangeWord(rng)+"）；级别和关键词不影响删除。").FontSize(11)
			if errText != "" {
				components.Text(c, errText).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "取消", components.ButtonGhost).Clicked() {
					vm.logMu.Lock()
					vm.LogClear = nil
					vm.logMu.Unlock()
					open = false
				}
				if components.Button(c, "确认清空", components.ButtonPrimary).Disabled(busy).Clicked() {
					vm.runLogClear()
				}
			})
		})
	})
	if !open {
		vm.logMu.Lock()
		vm.LogClear = nil
		vm.logMu.Unlock()
	}
}

// runLogClear deletes the range the dialog describes, keeping levels and
// keywords out of it: those narrow what is shown, not what is swept.
func (vm *ViewModel) runLogClear() {
	if vm.History == nil {
		return
	}
	vm.logMu.Lock()
	dialog := vm.LogClear
	if dialog == nil || dialog.Busy {
		vm.logMu.Unlock()
		return
	}
	dialog.Busy = true
	dialog.Error = ""
	query := core.LogQuery{DeviceID: dialog.Device, Since: logRangeSince(dialog.Range)}
	filter := dialog.filter
	vm.logMu.Unlock()

	go func() {
		_, err := vm.History.ClearLogs(query)
		vm.logMu.Lock()
		if current := vm.LogClear; current != nil {
			current.Busy = false
			if err != nil {
				current.Error = err.Error()
			} else {
				vm.LogClear = nil
			}
		}
		vm.logMu.Unlock()
		// The view re-reads either way: a partial failure still deleted rows.
		if filter != nil {
			vm.refreshLogs(filter)
		}
	}()
}

// logRangeSince is since() for a bare range index, so the clear dialog can
// describe the same range without holding a filter.
func logRangeSince(rng int) time.Time {
	return (&logFilter{Range: rng}).since()
}

// logRangeWord names a range for the dialog sentence.
func logRangeWord(rng int) string {
	if rng < 0 || rng >= len(logRangeLabels) {
		return logRangeLabels[logRangeToday]
	}
	return logRangeLabels[rng]
}
