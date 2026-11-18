package app

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

func TestLogPageHasAnEmptyState(t *testing.T) {
	vm, reader, _ := deviceLogFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "这段时间没有日志")
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

func TestLogPageEndsAboveThePageEdge(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	var rows []core.TimelineEntry
	for i := 0; i < 80; i++ {
		rows = append(rows, core.TimelineEntry{
			At: base.Add(time.Duration(-i) * time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: "E245", Text: "第" + itoa(i) + "行日志",
		})
	}
	reader.add(rows...)

	const window = 700
	tt := ui.NewTester(vm.ContentView, 900, window)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "第0行日志")
	// The footer row is the last thing the tab draws; the page's bottom
	// padding has to stay clear of the window edge.
	rect, ok := tt.Find("清空日志…")
	if !ok {
		t.Fatalf("no footer row; texts = %v", tt.Texts())
	}
	if bottom := rect.Y + rect.H; bottom > window-10 {
		t.Fatalf("the log tab ends at %v, past the page's bottom padding", bottom)
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

	if err := tt.Click("全部时间"); err != nil {
		t.Fatalf("Click(全部时间) error: %v", err)
	}
	tt.Frame()
	fifth := waitForQuery(t, reader, 5)
	if !fifth.Since.IsZero() {
		t.Fatalf("choosing 全部时间 still bounds the query from %v", fifth.Since)
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
	if first, _ := filter.table.Visible(); first != 0 {
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

// deviceLogFixture is a view model on one device's 日志 tab with a fake reader.
func deviceLogFixture(t *testing.T) (*ViewModel, *fakeLogReader, time.Time) {
	t.Helper()
	vm := newTestViewModel(t)
	reader := &fakeLogReader{outputs: map[uuid.UUID]string{}}
	vm.History = reader
	vm.DetailTab = detailLog
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	return vm, reader, time.Now()
}

func TestDeviceLogTabQueriesOnlyThatDevice(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	otherID, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: "第二台", Address: testAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	reader.add(
		core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: "E245", Text: "第一台的日志"},
		core.TimelineEntry{At: base.Add(-2 * time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: otherID, DeviceName: "第二台", Text: "第二台的日志"},
		core.TimelineEntry{At: base.Add(-3 * time.Minute), Level: core.LogError, Source: core.LogApp,
			Text: "应用级错误"},
	)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	query := waitForQuery(t, reader, 1)
	if query.DeviceID == nil {
		t.Fatal("the device tab queried every device; it must pass its own id")
	}
	if *query.DeviceID != profile.ID {
		t.Fatalf("the device tab queried %v, want %v", *query.DeviceID, profile.ID)
	}
	waitForText(t, tt, "第一台的日志")
	if tt.HasText("第二台的日志") {
		t.Fatalf("another device's line leaked into this tab; texts = %v", tt.Texts())
	}
	if tt.HasText("应用级错误") {
		t.Fatalf("an app line leaked into the device tab; texts = %v", tt.Texts())
	}

	// Each device keeps its own filter: opening the second one is not the
	// first one's query.
	vm.DetailTab = detailLog
	vm.OpenDevice(otherID)
	tt.Frame()
	second := waitForQuery(t, reader, 2)
	if second.DeviceID == nil || *second.DeviceID != otherID {
		t.Fatalf("the second device queried %v, want %v", second.DeviceID, otherID)
	}
	waitForText(t, tt, "第二台的日志")
}

func TestDeviceLogTabHidesTheDevicePicker(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", Text: "第一台的日志"})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "第一台的日志")
	if tt.HasText("全部设备与应用") || tt.HasText("只看应用") {
		t.Fatalf("the device tab shows the device picker; texts = %v", tt.Texts())
	}
}

func TestLogPageAndDeviceTabShareTheSameRows(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	reader.add(
		core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: "E245", Text: "通道已就绪"},
		core.TimelineEntry{At: base.Add(-2 * time.Minute), Level: core.LogInfo, Source: core.LogTask,
			DeviceID: profile.ID, DeviceName: "E245", TaskID: uuid.New(), Text: "adb pull · 失败"},
	)
	tab := ui.NewTester(vm.ContentView, 1100, 760)
	tab.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tab, "adb pull · 失败")
	tabTexts := tab.Texts()

	vm.OpenLogs()
	vm.pageLogFilter().Device = &profile.ID
	page := ui.NewTester(vm.ContentView, 1100, 760)
	page.Frame()
	waitForQuery(t, reader, 2)
	waitForText(t, page, "adb pull · 失败")
	for _, want := range []string{"通道已就绪", "adb pull · 失败"} {
		if !containsText(tabTexts, want) {
			t.Fatalf("the tab lost %q: %v", want, tabTexts)
		}
		if !page.HasText(want) {
			t.Fatalf("the page shows different rows for the same filter; missing %q", want)
		}
	}
}

func containsText(texts []string, want string) bool {
	for _, text := range texts {
		if text == want {
			return true
		}
	}
	return false
}

func TestDeviceLogTabKeepsFollowTheTail(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: profile.ID, DeviceName: "E245", Text: "第一行"})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	tt.Frame()

	filter := vm.logFilterFor(profile.ID)
	if !filter.Follow {
		t.Fatal("the device tab must follow the newest line by default")
	}
	reader.add(core.TimelineEntry{At: base.Add(time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: profile.ID, DeviceName: "E245", Text: "又一行"})
	vm.followLogs(filter)
	tt.Frame()
	waitForText(t, tt, "又一行")
	if first, _ := filter.table.Visible(); first != 0 {
		t.Fatalf("the followed tab starts at row %d, want the newest row", first)
	}
}

func TestLogsPageDoesNotRequeryOnEveryFrame(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogApp, Text: "一行"})
	vm.pageLogFilter().Range = logRangeWeek
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	for i := 0; i < 5; i++ {
		tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	if got := len(reader.recorded()); got != 1 {
		t.Fatalf("the page made %d queries while nothing changed, want 1", got)
	}
}

func TestLogFollowStopsWhenAnotherViewDraws(t *testing.T) {
	vm := newTestViewModel(t)
	device := vm.Manager.Profiles()[0].ID
	tab := vm.logFilterFor(device)
	tab.Follow = true
	vm.markLogVisible(tab)
	vm.ensureLogFollow(tab)
	vm.markLogVisible(vm.pageLogFilter())
	if vm.logFollowWanted(tab) {
		t.Fatal("the device tab is still wanted after the page took over")
	}

	// The harder case: no log view draws at all (the user went to the index
	// page). The next frame clears the active view, and the tab must stop.
	other := vm.logFilterFor(uuid.New())
	vm.markLogVisible(other)
	vm.clearActiveLog()
	if vm.logFollowWanted(other) {
		t.Fatal("a log view that is no longer drawn still wants to follow")
	}
}

func TestLogsPageKeepsTwoIdenticalLinesApart(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	at := base.Add(-time.Minute).Truncate(time.Millisecond)
	const text = "wg: [v2] Routine: receive incoming mkReceiveFunc - started"
	reader.add(
		core.TimelineEntry{Key: "log:1", At: at, Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", Text: text},
		core.TimelineEntry{Key: "log:2", At: at, Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: vm.Manager.Profiles()[0].ID, DeviceName: "E245", Text: text},
	)
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, text)
	// Two rows that differ only by their key must both render: the table's
	// row keys are the store's, not a guess from the content.
	if got := len(vm.pageLogFilter().entries); got != 2 {
		t.Fatalf("the page kept %d rows, want both identical lines", got)
	}
	rows, _ := tt.Find(text)
	if rows.Y == 0 {
		t.Fatal("the identical lines did not render")
	}
}

func TestFollowKeepsAnIdenticalNewLine(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	at := base.Add(-time.Minute).Truncate(time.Millisecond)
	const text = "重复一行"
	reader.add(core.TimelineEntry{Key: "log:1", At: at, Level: core.LogInfo, Source: core.LogApp, Text: text})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)

	filter := vm.pageLogFilter()
	filter.Follow = true
	// The same text at the same instant, one row later: a follow must not
	// mistake it for the row already on screen.
	reader.add(core.TimelineEntry{Key: "log:2", At: at, Level: core.LogInfo, Source: core.LogApp, Text: text})
	vm.followLogs(filter)
	if got := len(filter.entries); got != 2 {
		t.Fatalf("after following the page shows %d rows, want 2", got)
	}
}

func TestFollowSwitchCanBeTurnedOff(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: profile.ID, DeviceName: "E245", Text: "一行"})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	filter := vm.logFilterFor(profile.ID)
	if !filter.Follow {
		t.Fatal("the device tab must follow by default")
	}
	if err := tt.Click("跟随最新"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	tt.Frame()
	if filter.Follow {
		t.Fatal("跟随最新 cannot be turned off")
	}
	if err := tt.Click("跟随最新"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	tt.Frame()
	if !filter.Follow {
		t.Fatal("跟随最新 cannot be turned back on")
	}
}

func TestLogPageFillsTheTimelineBoxWhenEmpty(t *testing.T) {
	vm, reader, _ := logsPageFixture(t)
	const window = 760
	tt := ui.NewTester(vm.ContentView, 1100, window)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "这段时间没有日志")

	note, ok := tt.Find("这段时间没有日志")
	if !ok {
		t.Fatalf("no empty note; texts = %v", tt.Texts())
	}
	if note.H < 20 {
		t.Fatalf("the empty note is %0.0f high; it must be larger than body text", note.H)
	}
	// Centred in the box: the note's middle is near the middle of the space
	// between the filter rows and the footer.
	footer, ok := tt.Find("清空日志…")
	if !ok {
		t.Fatalf("no footer row; texts = %v", tt.Texts())
	}
	if middle := note.Y + note.H/2; middle < window*0.3 || middle > window*0.8 {
		t.Fatalf("the empty note sits at y=%0.0f, not in the middle of the box", middle)
	}
	// The footer stays at the bottom even when there is nothing to show.
	if bottom := footer.Y + footer.H; bottom < window-60 {
		t.Fatalf("清空日志 ends at y=%0.0f, want it pinned to the bottom", bottom)
	}
}

func TestLogFootRowHoldsRefreshLeftOfClear(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	reader.add(core.TimelineEntry{At: base.Add(-time.Minute), Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: profile.ID, DeviceName: "E245", Text: "一行"})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "一行")

	refresh, ok := tt.Find("刷新")
	if !ok {
		t.Fatalf("no refresh button; texts = %v", tt.Texts())
	}
	clear, ok := tt.Find("清空日志…")
	if !ok {
		t.Fatalf("no clear button; texts = %v", tt.Texts())
	}
	if refresh.Y < clear.Y-2 || refresh.Y > clear.Y+2 {
		t.Fatalf("刷新 is at y=%0.0f and 清空日志 at y=%0.0f; they share the footer row", refresh.Y, clear.Y)
	}
	if refresh.X+refresh.W > clear.X+1 {
		t.Fatalf("刷新 (x=%0.0f..%0.0f) must sit left of 清空日志 (x=%0.0f)", refresh.X, refresh.X+refresh.W, clear.X)
	}
	// The filter rows no longer carry the primary action.
	rows, ok := tt.Find("级别")
	if ok && refresh.Y < rows.Y+rows.H {
		t.Fatalf("刷新 is still up with the filters at y=%0.0f", refresh.Y)
	}
}

// TestLogsPageUsesNoOnevraTable keeps the timeline on the one table card.
func TestLogsPageUsesNoOnevraTable(t *testing.T) {
	assertNoOnevraTable(t, "logs.go")
}

// TestLogTimelineIsItsOwnScrollArea pins the timeline's layout: it is a labelled
// table that scrolls its own rows, so a long text column scrolls sideways
// instead of pushing the header out of reach.
func TestLogTimelineIsItsOwnScrollArea(t *testing.T) {
	vm, reader, base := deviceLogFixture(t)
	profile := vm.Manager.Profiles()[0]
	var rows []core.TimelineEntry
	for i := 0; i < 80; i++ {
		rows = append(rows, core.TimelineEntry{
			At: base.Add(time.Duration(-i) * time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: "E245", Text: "第" + itoa(i) + "行日志",
		})
	}
	reader.add(rows...)

	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "第0行日志")
	timeline, ok := tt.Find("日志时间线")
	if !ok {
		t.Fatalf("the timeline is not a labelled table; texts = %v", tt.Texts())
	}
	if tt.HasText("第79行日志") {
		t.Fatal("the timeline built a row it does not show")
	}
	tt.Scroll(timeline.X+timeline.W/2, timeline.Y+timeline.H/2, 0, 4000)
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	waitForText(t, tt, "第79行日志")
}
