package app

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

func TestDeviceTimelineMergesLogsAndTasksInOrder(t *testing.T) {
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	session := core.Session{Logs: []core.SessionLog{
		{At: base.Add(2 * time.Second), Text: "通道已就绪"},
		{At: base.Add(4 * time.Second), Text: "adb 已连接"},
	}}
	tasks := []core.Task{{
		ID: uuid.New(), Title: "adb shell cat /proc/meminfo",
		Started: base.Add(3 * time.Second), State: core.TaskSucceeded,
	}}
	got := logText(deviceTimeline(session, tasks))
	want := strings.Join([]string{
		"12:00:02 通道已就绪",
		"12:00:03 adb shell cat /proc/meminfo · 已完成",
		"12:00:04 adb 已连接",
	}, "\n")
	if got != want {
		t.Fatalf("log text = %q, want %q", got, want)
	}
}

func TestDeviceTasksFiltersByDevice(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	tasks := []core.Task{
		{ID: uuid.New(), DeviceID: mine, Title: "上传"},
		{ID: uuid.New(), DeviceID: other, Title: "别的设备的任务"},
	}
	kept := deviceTasks(tasks, mine)
	if len(kept) != 1 || kept[0].Title != "上传" {
		t.Fatalf("deviceTasks() = %+v, want only this device's task", kept)
	}
}

func TestLogTextKeepsEveryLineOfAFullWindow(t *testing.T) {
	session := core.Session{}
	for i := 0; i < 200; i++ {
		session.Logs = append(session.Logs, core.SessionLog{Text: "第" + itoa(i) + "行"})
	}
	tasks := make([]core.Task, 50)
	for i := range tasks {
		tasks[i] = core.Task{DeviceID: uuid.New(), Title: "任务" + itoa(i)}
	}
	if lines := strings.Split(logText(deviceTimeline(session, tasks)), "\n"); len(lines) != 250 {
		t.Fatalf("log text has %d lines, want 250", len(lines))
	}
}

func TestLogPageHasAnEmptyState(t *testing.T) {
	vm := newTestViewModel(t)
	vm.DetailTab = detailLog
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("暂无通道日志与任务") {
		t.Fatalf("no empty state on the log page; texts = %v", tt.Texts())
	}
}

func TestLogPageKeepsBuffersSeparatePerDevice(t *testing.T) {
	vm := newTestViewModel(t)
	first := vm.Manager.Profiles()[0]
	secondID, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: "第二台", Address: testAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.logPageText(first.ID, core.Session{Logs: []core.SessionLog{{Text: "第一台的日志"}}})
	vm.logPageText(secondID, core.Session{Logs: []core.SessionLog{{Text: "第二台的日志"}}})
	if got := vm.logState(first.ID).text; strings.Contains(got, "第二台") {
		t.Fatalf("device A's buffer holds device B's lines: %q", got)
	}
	if got := vm.logState(secondID).text; strings.Contains(got, "第一台") {
		t.Fatalf("device B's buffer holds device A's lines: %q", got)
	}
}

func TestLogPageStopsFollowingAfterScrollUp(t *testing.T) {
	vm := newTestViewModel(t)
	id := vm.Manager.Profiles()[0].ID
	// 第一次刷新：空缓冲必须跟到尾部，这里先把内容与"在尾部"的状态建好。
	vm.logPageText(id, core.Session{Logs: []core.SessionLog{{Text: "第一行"}}})
	state := vm.logState(id)
	if !state.follow {
		t.Fatal("the first refresh must follow the tail")
	}
	state.scroll.Y, state.scroll.MaxY = 0, 400
	vm.logPageText(id, core.Session{Logs: []core.SessionLog{{Text: "新的一行"}}})
	if state.follow {
		t.Fatal("scrolling up must stop the log page from following the tail")
	}
	state.scroll.Y = 396
	vm.logPageText(id, core.Session{Logs: []core.SessionLog{{Text: "又一行"}}})
	if !state.follow {
		t.Fatal("scrolling back to the bottom must resume following")
	}
}

func TestLogPageCancelsTheRunningTask(t *testing.T) {
	vm := newBlockingViewModel(t, &blockingRunner{release: make(chan struct{})})
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "adb install", []string{"install", "-r", "app.apk"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	vm.DetailTab = detailLog
	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("运行中 1 个任务") {
		t.Fatalf("the log page does not show the running task; texts = %v", tt.Texts())
	}
	if err := tt.Click("取消全部"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	vm.Manager.WaitTask(taskID)
	if got := vm.Manager.Tasks()[0].State; got != core.TaskCancelled {
		t.Fatalf("task state = %v, want %v", got, core.TaskCancelled)
	}
}

func TestRunningTasksFiltersByDeviceAndState(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	tasks := []core.Task{
		{ID: uuid.New(), DeviceID: mine, State: core.TaskRunning},
		{ID: uuid.New(), DeviceID: mine, State: core.TaskSucceeded},
		{ID: uuid.New(), DeviceID: other, State: core.TaskRunning},
	}
	if got := runningTasks(tasks, mine); len(got) != 1 {
		t.Fatalf("runningTasks() = %+v, want only this device's running task", got)
	}
}

func TestLogPagePausesWhileReadingUp(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	session := core.Session{}
	for i := 0; i < 200; i++ {
		session.Logs = append(session.Logs, core.SessionLog{At: base.Add(time.Duration(i) * time.Second), Text: "第" + itoa(i) + "行"})
	}
	// The page runs against this snapshot on every frame, the way the device
	// page passes the manager's session.
	tt := ui.NewTester(func(c *ui.Context) { vm.logPage(c, profile, session) }, 900, 700)
	tt.Frame()
	state := vm.logState(profile.ID)
	if state.scroll.MaxY == 0 {
		t.Fatal("the log page does not scroll")
	}

	state.scroll.Y = 0
	tt.Frame()
	if state.follow {
		t.Fatal("scrolling up must stop following")
	}
	session.Logs = append(session.Logs, core.SessionLog{At: base.Add(time.Hour), Text: "新的一行"})
	tt.Frame()
	// Rewriting the buffer moves the caret to the tail and drags the view
	// with it, so a paused page must not touch the text at all.
	if strings.Contains(state.text, "新的一行") {
		t.Fatal("a paused log page rewrote the text under the reader")
	}
	if state.scroll.Y > 1 {
		t.Fatalf("the view jumped to y=%.0f while the reader was scrolled up", state.scroll.Y)
	}

	state.scroll.Y = math.MaxFloat32
	tt.Frame()
	if !strings.Contains(state.text, "新的一行") {
		t.Fatalf("returning to the tail did not catch up: %q", state.text)
	}
}

func TestLogPageEndsAboveThePageEdge(t *testing.T) {
	vm, _ := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		if strings.Contains(strings.Join(c.Args, " "), "get-state") {
			return core.Result{Output: "device\n"}, nil
		}
		return core.Result{}, nil
	})
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	state := vm.logState(profile.ID)
	state.text = ""
	var lines []logLine
	base := time.Now()
	for i := 0; i < 80; i++ {
		lines = append(lines, logLine{At: base.Add(time.Duration(i) * time.Second), Text: "第" + itoa(i) + "行日志"})
	}
	state.buffer.Set(logText(lines))
	vm.DetailTab = detailLog
	vm.OpenDevice(profile.ID)

	const window = 700
	tt := ui.NewTester(vm.ContentView, 900, window)
	rect, ok := tt.Find("通道日志与任务内容")
	if !ok {
		t.Fatalf("no log text area; texts = %v", tt.Texts())
	}
	if bottom := rect.Y + rect.H; bottom > window-20 {
		t.Fatalf("the log area ends at %v, past the page's bottom padding", bottom)
	}
}

// fakeLogReader answers the log page and records what it was asked, so a test
// can prove the page queries what the user chose.
type fakeLogReader struct {
	mu        sync.Mutex
	queries   []core.LogQuery
	rows      []core.TimelineEntry
	outputs   map[uuid.UUID]string
	err       error
	clearErrs []error
	clears    []core.LogQuery
}

func (r *fakeLogReader) Timeline(query core.LogQuery) ([]core.TimelineEntry, error) {
	r.mu.Lock()
	r.queries = append(r.queries, query)
	rows := append([]core.TimelineEntry(nil), r.rows...)
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = core.DefaultLogLimit
	}
	var out []core.TimelineEntry
	for _, row := range rows {
		if query.DeviceID != nil {
			if *query.DeviceID == uuid.Nil {
				if row.DeviceID != uuid.Nil {
					continue
				}
			} else if row.DeviceID != *query.DeviceID {
				continue
			}
		}
		if query.Level != "" && row.Level != query.Level {
			continue
		}
		if !query.Since.IsZero() && row.At.Before(query.Since) {
			continue
		}
		if !demoMatchesAll([]string{row.Text}, strings.Fields(query.Search)) {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClearLogs deletes the log rows in range and returns how many went; task rows
// are untouched, which is what the page promises.
func (r *fakeLogReader) ClearLogs(query core.LogQuery) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clears = append(r.clears, query)
	if len(r.clearErrs) > 0 {
		err := r.clearErrs[0]
		r.clearErrs = r.clearErrs[1:]
		return 0, err
	}
	kept := r.rows[:0]
	var removed int64
	for _, row := range r.rows {
		if row.Source == core.LogTask {
			kept = append(kept, row)
			continue
		}
		if query.DeviceID != nil && row.DeviceID != *query.DeviceID {
			kept = append(kept, row)
			continue
		}
		if !query.Since.IsZero() && row.At.Before(query.Since) {
			kept = append(kept, row)
			continue
		}
		removed++
	}
	r.rows = kept
	return removed, nil
}

func (r *fakeLogReader) clearedQueries() []core.LogQuery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.LogQuery(nil), r.clears...)
}

func (r *fakeLogReader) TaskOutput(id uuid.UUID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	output, ok := r.outputs[id]
	if !ok {
		return "", core.Error(core.MsgTaskOutputMissing)
	}
	return output, nil
}

func (r *fakeLogReader) add(rows ...core.TimelineEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, rows...)
}

func (r *fakeLogReader) recorded() []core.LogQuery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.LogQuery(nil), r.queries...)
}

func (r *fakeLogReader) last() (core.LogQuery, bool) {
	queries := r.recorded()
	if len(queries) == 0 {
		return core.LogQuery{}, false
	}
	return queries[len(queries)-1], true
}

// waitForQuery blocks until the reader has answered n queries.
func waitForQuery(t *testing.T, reader *fakeLogReader, n int) core.LogQuery {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if queries := reader.recorded(); len(queries) >= n {
			return queries[n-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log page made %d queries, want %d", len(reader.recorded()), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSidebarHasTheLogsEntry(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	device, ok := tt.Find("E245")
	if !ok {
		t.Fatalf("device row missing; texts = %v", tt.Texts())
	}
	entry, ok := tt.Find("日志")
	if !ok {
		t.Fatalf("the logs entry is missing; texts = %v", tt.Texts())
	}
	if entry.Y <= device.Y {
		t.Fatalf("日志 is above the device list (%0.0f <= %0.0f)", entry.Y, device.Y)
	}
	if err := tt.Click("日志"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != LogsRoute {
		t.Fatalf("path = %q, want %q", got, LogsRoute)
	}
}

func TestLogsPageTitleHasABreadcrumbBack(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenLogs()
	title := ui.NewTester(vm.ContentTitle, 400, 40)
	if !title.HasText("我的设备") || !title.HasText("日志") {
		t.Fatalf("title = %v, want 我的设备 › 日志", title.Texts())
	}
}

func TestLogsPageListsAppAndDeviceRows(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	reader.add(
		core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", Text: "通道已就绪"},
		core.TimelineEntry{At: base.Add(-2 * time.Minute), Level: core.LogError, Source: core.LogApp,
			Text: "配置读取失败"},
		core.TimelineEntry{At: base.Add(-3 * time.Minute), Level: core.LogInfo, Source: core.LogTask,
			DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", TaskID: uuid.New(),
			Text: "adb pull /sdcard/x · 失败"},
	)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "配置读取失败")
	for _, want := range []string{"通道已就绪", "adb pull /sdcard/x · 失败", "错误", "通道", "任务", "E245"} {
		if !tt.HasText(want) {
			t.Fatalf("%q missing from the log page; texts = %v", want, tt.Texts())
		}
	}
}

func TestLogsPageFiltersCallReaderWithChosenValues(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogError, Source: core.LogApp, Text: "配置读取失败"})
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	first := waitForQuery(t, reader, 1)
	if first.DeviceID != nil {
		t.Fatalf("the page opened filtered to %v, want every device", first.DeviceID)
	}
	if first.Since.IsZero() {
		t.Fatal("the default range must carry a lower bound")
	}

	if err := tt.Click("错误"); err != nil {
		t.Fatalf("Click(错误) error: %v", err)
	}
	tt.Frame()
	second := waitForQuery(t, reader, 2)
	if second.Level != core.LogError {
		t.Fatalf("level query = %q, want error", second.Level)
	}

	vm.pageLogFilter().Search = "超时"
	tt.Frame()
	third := waitForQuery(t, reader, 3)
	if third.Search != "超时" {
		t.Fatalf("search query = %q, want 超时", third.Search)
	}

	vm.pageLogFilter().Device = &profile.ID
	tt.Frame()
	fourth := waitForQuery(t, reader, 4)
	if fourth.DeviceID == nil || *fourth.DeviceID != profile.ID {
		t.Fatalf("device query = %v, want %v", fourth.DeviceID, profile.ID)
	}

	if err := tt.Click("全部"); err != nil {
		t.Fatalf("Click(全部) error: %v", err)
	}
	tt.Frame()
	fifth := waitForQuery(t, reader, 5)
	if fifth.Since.IsZero() {
		t.Fatal("choosing 全部 must lift the time lower bound")
	}
}

func TestLogsPageExplainsAnEmptyRange(t *testing.T) {
	vm, reader, _ := logsPageFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	tt.Frame()
	if !tt.HasText("这段时间没有日志") {
		t.Fatalf("no empty state; texts = %v", tt.Texts())
	}
}

func TestFollowQueriesOnlyNewRows(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	newest := base.Add(-time.Minute)
	reader.add(core.TimelineEntry{At: newest, Level: core.LogInfo, Source: core.LogApp, Text: "第一条"})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)

	filter := vm.pageLogFilter()
	filter.Follow = true
	reader.add(core.TimelineEntry{At: newest.Add(time.Minute), Level: core.LogError, Source: core.LogApp, Text: "新的一行"})
	vm.followLogs(filter)

	query := waitForQuery(t, reader, 2)
	if !query.Since.Equal(newest) {
		t.Fatalf("the follow query asked since %v, want the last row's time %v", query.Since, newest)
	}
	tt.Frame()
	if !tt.HasText("新的一行") {
		t.Fatalf("the new row did not arrive; texts = %v", tt.Texts())
	}
	if tt.HasText("第一条") == false {
		t.Fatal("following dropped the rows already shown")
	}
}

func TestLogsPageKeepsTheReaderAtTheTail(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	var rows []core.TimelineEntry
	for i := 0; i < 40; i++ {
		rows = append(rows, core.TimelineEntry{
			At: base.Add(time.Duration(-i) * time.Minute), Level: core.LogInfo, Source: core.LogApp,
			Text: "第" + itoa(i) + "行",
		})
	}
	reader.add(rows...)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	tt.Frame()

	filter := vm.pageLogFilter()
	filter.Follow = true
	reader.add(core.TimelineEntry{At: base.Add(time.Minute), Level: core.LogError, Source: core.LogApp, Text: "最新一行"})
	vm.followLogs(filter)
	tt.Frame()
	waitForText(t, tt, "最新一行")
	// The newest row is at the top, so "at the tail" means the table still
	// shows the top of the list rather than staying where the reader was.
	if first, _ := filter.table.List.Visible(); first != 0 {
		t.Fatalf("the followed table starts at row %d, want the newest row", first)
	}
}

// logsPageFixture is a view model on the log page with a fake reader.
func logsPageFixture(t *testing.T) (*ViewModel, *fakeLogReader, time.Time) {
	t.Helper()
	vm := newTestViewModel(t)
	reader := &fakeLogReader{outputs: map[uuid.UUID]string{}}
	vm.History = reader
	vm.OpenLogs()
	return vm, reader, time.Now()
}

func TestLogsPageShowsTaskOutput(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	profile := vm.Manager.Profiles()[0]
	taskID := uuid.New()
	reader.outputs[taskID] = "adb: error: failed to stat remote object: No such file or directory"
	reader.add(core.TimelineEntry{
		At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogTask,
		DeviceID: profile.ID, DeviceName: "E245", TaskID: taskID,
		Text: "adb pull /sdcard/x · 失败",
	})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "adb pull /sdcard/x · 失败")
	if err := tt.Click("adb pull /sdcard/x · 失败"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	waitForText(t, tt, "No such file or directory")
	if !tt.HasText("任务输出") {
		t.Fatalf("the output dialog has no title; texts = %v", tt.Texts())
	}
}

func TestClearLogsAsksAndKeepsTasks(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	profile := vm.Manager.Profiles()[0]
	reader.add(
		core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: "E245", Text: "通道已就绪"},
		core.TimelineEntry{At: base.Add(-2 * time.Minute), Level: core.LogInfo, Source: core.LogTask,
			DeviceID: profile.ID, DeviceName: "E245", TaskID: uuid.New(), Text: "adb pull · 失败"},
	)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	tt.Frame()

	if err := tt.Click("清空日志…"); err != nil {
		t.Fatalf("Click(清空日志…) error: %v", err)
	}
	waitForText(t, tt, "任务记录不会被删除")
	if err := tt.Click("确认清空"); err != nil {
		t.Fatalf("Click(确认清空) error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(reader.clearedQueries()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the clear never reached the reader")
		}
		tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	query := reader.clearedQueries()[0]
	if query.DeviceID != nil {
		t.Fatalf("the page cleared only %v, want every device", query.DeviceID)
	}
	if query.Since.IsZero() {
		t.Fatal("the clear ignored the chosen range")
	}
	// The task row is not a log row, so it survives.
	tt.Frame()
	if !tt.HasText("adb pull · 失败") {
		t.Fatalf("清空 removed the task rows too; texts = %v", tt.Texts())
	}
	if tt.HasText("通道已就绪") {
		t.Fatalf("the cleared channel line is still on the page; texts = %v", tt.Texts())
	}
}

func TestClearFailureIsShownInTheDialog(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", Text: "通道已就绪"})
	reader.clearErrs = []error{core.Error("历史库已关闭")}
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	tt.Frame()
	if err := tt.Click("清空日志…"); err != nil {
		t.Fatalf("Click(清空日志…) error: %v", err)
	}
	if err := tt.Click("确认清空"); err != nil {
		t.Fatalf("Click(确认清空) error: %v", err)
	}
	waitForText(t, tt, "历史库已关闭")
}
