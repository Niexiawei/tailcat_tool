package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// logcatRunner is a runner whose logcat streams never run anything real: Start
// records the command and hands back both a feed and a Proc a test can end.
type logcatRunner struct {
	mu     sync.Mutex
	starts []core.Command
	feeds  []func(string)
	procs  []*core.Proc
	dones  []chan int
	// startErr makes every Start fail, for the page's "cannot start" state.
	startErr error
}

func (r *logcatRunner) Start(_ context.Context, c core.Command, onOutput func(string)) (*core.Proc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan int, 1)
	proc := &core.Proc{Done: done}
	r.starts = append(r.starts, c)
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.feeds = append(r.feeds, onOutput)
	r.procs = append(r.procs, proc)
	r.dones = append(r.dones, done)
	return proc, nil
}

func (r *logcatRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg == "get-state" {
			return core.Result{Output: "device\n"}, nil
		}
	}
	if strings.Contains(strings.Join(c.Args, " "), "ps -A") {
		return core.Result{Output: "USER PID PPID VSZ RSS WCHAN ADDR S NAME\n" +
			"u0_a132 4100 500 1 2 3 4 S com.demo.camera\n" +
			"u0_a133 4200 500 1 2 3 4 S com.demo.gallery\n" +
			"system 850 1 1 2 3 4 S system_server\n" +
			"root 1234 1 1 2 3 4 S surfaceflinger\n" +
			"root 505 1 1 2 3 4 S wifi_forwarder\n"}, nil
	}
	return core.Result{}, nil
}

// attempts is how many streams the page asked for, failures included.
func (r *logcatRunner) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts)
}

// logcatCalls keeps only the streaming commands.
func (r *logcatRunner) logcatCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.starts {
		joined := strings.Join(c.Args, " ")
		if strings.Contains(joined, " logcat ") {
			out = append(out, joined)
		}
	}
	return out
}

// feed hands text to the most recent stream, the way a pipe would.
func (r *logcatRunner) feed(text string) {
	r.mu.Lock()
	feed := r.feeds[len(r.feeds)-1]
	r.mu.Unlock()
	feed(text)
}

// end closes the most recent stream's process.
func (r *logcatRunner) end(code int) {
	r.mu.Lock()
	done := r.dones[len(r.dones)-1]
	r.mu.Unlock()
	done <- code
}

// newLogcatFixture is a view model whose first device is connected and whose
// page is already open.
func newLogcatFixture(t *testing.T) (*ViewModel, *logcatRunner) {
	t.Helper()
	runner := &logcatRunner{}
	vm := newBlockingViewModel(t, runner)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Disconnect(context.Background(), profile.ID) })
	vm.OpenLogcat()
	return vm, runner
}

func TestSidebarListsTheLogcatPage(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 900)
	if !tt.HasText("Logcat") {
		t.Fatalf("the sidebar has no Logcat entry; texts = %v", tt.Texts())
	}
	if err := tt.Click("Logcat"); err != nil {
		t.Fatalf("Click(Logcat) error: %v", err)
	}
	if got := vm.Router.Path(); got != LogcatRoute {
		t.Fatalf("path = %q, want %q", got, LogcatRoute)
	}
}

func TestLogcatPageListsOnlyConnectedDevices(t *testing.T) {
	vm := newTestViewModel(t)
	offlineID, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: "第二台", Address: testAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	_ = offlineID
	online := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), online.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), online.ID)

	devices := vm.onlineDevices()
	if len(devices) != 1 {
		t.Fatalf("onlineDevices() = %+v, want only the connected device", devices)
	}
	if devices[0].ID != online.ID {
		t.Fatalf("onlineDevices() = %q, want %q", devices[0].Name, online.Name)
	}
}

func TestLogcatPageExplainsThereIsNothingToStream(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenLogcat()
	tt := ui.NewTester(vm.ContentView, 1000, 720)
	tt.Frame()
	if !tt.HasText("没有已连接的设备，先到设备页连接一台。") {
		t.Fatalf("the page does not explain the empty state; texts = %v", tt.Texts())
	}
	if vm.Logcat == nil || vm.Logcat.stream != nil {
		t.Fatal("the page started a stream without a connected device")
	}
}

func TestLogcatPageStartsAStreamForTheFirstConnectedDevice(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 720)
	tt.Frame()
	calls := runner.logcatCalls()
	if len(calls) != 1 {
		t.Fatalf("started %d streams, want 1: %v", len(calls), calls)
	}
	serial := vm.Manager.Session(vm.Manager.Profiles()[0].ID).Serial
	if want := "-s " + serial + " logcat -v threadtime"; calls[0] != want {
		t.Fatalf("stream command = %q, want %q", calls[0], want)
	}
}

func TestLeavingTheLogcatPageStopsTheStream(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 720)
	tt.Frame()
	stream := vm.Logcat.stream
	if stream == nil {
		t.Fatal("the page never started its stream")
	}
	vm.OpenDevices()
	tt.Frame()
	if !stream.Closed() {
		t.Fatal("the stream is still running although the page was left")
	}
	if len(runner.logcatCalls()) != 1 {
		t.Fatalf("leaving the page started another stream: %v", runner.logcatCalls())
	}
}

func TestLogcatPageShowsTheEndedStream(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 720)
	waitForText(t, tt, "正在接收日志…")
	runner.end(0)
	waitForText(t, tt, "日志流已结束")
}

func TestLogcatPageShowsParsedEntries(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1234  1250 W ActivityManager: Started proc\n")
	waitForText(t, tt, "Started proc")
	for _, want := range []string{"09:30:01.123", "警告", "1234", "ActivityManager", "已接收 1 行"} {
		if !tt.HasText(want) {
			t.Fatalf("the page has no %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestLogcatLevelTabsKeepThatLevelAndAbove(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: info line\n" +
		"11-18 09:30:02.000  1  1 W Tag: warn line\n" +
		"11-18 09:30:03.000  1  1 E Tag: error line\n")
	waitForText(t, tt, "error line")
	if err := tt.Click("错误"); err != nil {
		t.Fatalf("Click(错误) error: %v", err)
	}
	waitForText(t, tt, "error line")
	tt.Frame()
	for _, unwanted := range []string{"info line", "warn line"} {
		if tt.HasText(unwanted) {
			t.Fatalf("%q passed a 错误 floor; texts = %v", unwanted, tt.Texts())
		}
	}
}

func TestLogcatTagAndKeywordBoxesFilterTheBuffer(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I ActivityManager: started proc\n" +
		"11-18 09:30:02.000  1  1 I OpenGLRenderer: draw frame\n")
	waitForText(t, tt, "draw frame")

	vm.Logcat.Filter.Tags = "activity"
	tt.Frame()
	if tt.HasText("draw frame") || !tt.HasText("started proc") {
		t.Fatalf("the tag box did not narrow the rows; texts = %v", tt.Texts())
	}

	vm.Logcat.Filter.Tags = ""
	vm.Logcat.Filter.Terms = "draw"
	tt.Frame()
	if tt.HasText("started proc") || !tt.HasText("draw frame") {
		t.Fatalf("the keyword box did not narrow the rows; texts = %v", tt.Texts())
	}
}

func TestLogcatFilterChangeKeepsTheBuffer(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: info line\n")
	waitForText(t, tt, "info line")
	if err := tt.Click("错误"); err != nil {
		t.Fatalf("Click(错误) error: %v", err)
	}
	if tt.HasText("info line") {
		t.Fatalf("a 错误 floor kept an information line; texts = %v", tt.Texts())
	}
	if err := tt.Click("全部"); err != nil {
		t.Fatalf("Click(全部) error: %v", err)
	}
	waitForText(t, tt, "info line")
}

func TestLogcatPauseFreezesTheRows(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: before pause\n")
	waitForText(t, tt, "before pause")
	if err := tt.Click("暂停"); err != nil {
		t.Fatalf("Click(暂停) error: %v", err)
	}
	feedAfterPause := func() {
		runner.feed("11-18 09:30:02.000  1  1 I Tag: after pause\n")
		for i := 0; i < 5; i++ {
			tt.Frame()
		}
	}
	feedAfterPause()
	if tt.HasText("after pause") {
		t.Fatalf("a paused page moved under the reader; texts = %v", tt.Texts())
	}
	if !tt.HasText("已暂停") {
		t.Fatalf("a paused page does not say so; texts = %v", tt.Texts())
	}
	if err := tt.Click("继续"); err != nil {
		t.Fatalf("Click(继续) error: %v", err)
	}
	waitForText(t, tt, "after pause")
}

func TestLogcatFollowKeepsTheNewestLineInView(t *testing.T) {
	vm, _ := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	if !vm.Logcat.Follow || !vm.Logcat.table.FollowEnd {
		t.Fatalf("a fresh page does not follow the newest line (Follow = %v)", vm.Logcat.Follow)
	}
	if err := tt.Click("跟随最新"); err != nil {
		t.Fatalf("Click(跟随最新) error: %v", err)
	}
	tt.Frame()
	if vm.Logcat.Follow || vm.Logcat.table.FollowEnd {
		t.Fatal("turning 跟随最新 off did not stop the table following its end")
	}
}

func TestLogcatFootRowCountsReceivedAndDropped(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: one\n")
	waitForText(t, tt, "已接收 1 行 · 显示 1 条")

	// One line more than the ring holds: the page has to say what it lost.
	// Each line needs a stamp of its own: one call's lines share a stamp and
	// fold into a single entry.
	burst := strings.Builder{}
	for i := 0; i < core.LogcatMaxLines+1; i++ {
		burst.WriteString(fmt.Sprintf("11-18 09:30:01.%03d  1  1 I Tag: line\n", i%1000))
	}
	runner.feed(burst.String())
	// The foot row names the buffer, so "丢弃" is a number with a reason and
	// not a mystery.
	waitForText(t, tt, "丢弃 2 条（上限 30000 条 / 16 MB）")
}

func TestLogcatPageExplainsAnEmptyFilter(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: something\n")
	waitForText(t, tt, "something")
	vm.Logcat.Filter.Terms = "nothing-matches-this"
	waitForText(t, tt, "没有符合筛选条件的日志")
}

func TestLogcatClearEmptiesTheTable(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: will be cleared\n")
	waitForText(t, tt, "will be cleared")
	if err := tt.Click("清空缓冲"); err != nil {
		t.Fatalf("Click(清空缓冲) error: %v", err)
	}
	tt.Frame()
	if tt.HasText("will be cleared") {
		t.Fatalf("the cleared line is still on screen; texts = %v", tt.Texts())
	}
	// The counters describe the buffer, so they go with it: 「显示 0 条」 beside
	// 「已接收 1 行」 reads as one lost line.
	if !tt.HasText("已接收 0 行 · 显示 0 条") {
		t.Fatalf("clearing left the counters behind; texts = %v", tt.Texts())
	}
}

func TestLogcatPageRebuildsAnEndedStream(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.end(0)
	waitForText(t, tt, "日志流已结束")
	if err := tt.Click("重新开始"); err != nil {
		t.Fatalf("Click(重新开始) error: %v", err)
	}
	waitForText(t, tt, "正在接收日志…")
	if calls := runner.logcatCalls(); len(calls) != 2 {
		t.Fatalf("restarting started %d streams, want a second one: %v", len(calls), calls)
	}
}

func TestDemoLogcatStreamFeedsSyntheticLines(t *testing.T) {
	vm := newDemoViewModel(t, func(vm *ViewModel) {
		profile := vm.Manager.Profiles()[0]
		if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
			t.Fatalf("Connect() error: %v", err)
		}
		t.Cleanup(func() { vm.Manager.Disconnect(context.Background(), profile.ID) })
		vm.OpenLogcat()
	})
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	waitForText(t, tt, "演示日志")
}

// stubSaveDialog replaces the system save dialog and records what it was asked.
func stubSaveDialog(t *testing.T, target string) *int {
	t.Helper()
	calls := 0
	original := saveLocalPath
	t.Cleanup(func() { saveLocalPath = original })
	saveLocalPath = func(mygo.SaveDialogOptions) (string, error) {
		calls++
		return target, nil
	}
	return &calls
}

// waitForFile reads path once the export goroutine wrote it.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		// The file exists a moment before its content does: wait for the
		// content, not for the name.
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("never wrote %s: %v", path, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestLogcatExportWritesTheVisibleRows(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1234  1250 I ActivityManager: Started proc\n" +
		"11-18 09:30:02.000  1234  1250 W demo.camera: preview dropped\n")
	waitForText(t, tt, "preview dropped")

	target := filepath.Join(t.TempDir(), "logcat.txt")
	stubSaveDialog(t, target)
	if err := tt.Click("导出…"); err != nil {
		t.Fatalf("Click(导出…) error: %v", err)
	}
	text := waitForFile(t, target)
	year := time.Now().Year()
	for _, want := range []string{
		itoa(year) + "-11-18 09:30:01.123 I/ActivityManager(1234): Started proc\n",
		itoa(year) + "-11-18 09:30:02.000 W/demo.camera(1234): preview dropped\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the file has no %q; content = %q", want, text)
		}
	}
}

func TestLogcatExportKeepsTheFilter(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: info line\n" +
		"11-18 09:30:02.000  1  1 E Tag: error line\n")
	waitForText(t, tt, "error line")
	if err := tt.Click("错误"); err != nil {
		t.Fatalf("Click(错误) error: %v", err)
	}
	target := filepath.Join(t.TempDir(), "filtered.txt")
	stubSaveDialog(t, target)
	if err := tt.Click("导出…"); err != nil {
		t.Fatalf("Click(导出…) error: %v", err)
	}
	text := waitForFile(t, target)
	if strings.Contains(text, "info line") || !strings.Contains(text, "error line") {
		t.Fatalf("the export ignored the filter; content = %q", text)
	}
}

func TestLogcatExportCancelledWritesNothing(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: a line\n")
	waitForText(t, tt, "a line")

	dir := t.TempDir()
	calls := stubSaveDialog(t, "")
	if err := tt.Click("导出…"); err != nil {
		t.Fatalf("Click(导出…) error: %v", err)
	}
	tt.Frame()
	if *calls != 1 {
		t.Fatalf("the save dialog opened %d times, want 1", *calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a cancelled export wrote %d files", len(entries))
	}
}

func TestLogcatExportRefusesAnEmptyTable(t *testing.T) {
	vm, _ := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	calls := stubSaveDialog(t, filepath.Join(t.TempDir(), "empty.txt"))
	if err := tt.Click("导出…"); err != nil {
		t.Fatalf("Click(导出…) error: %v", err)
	}
	tt.Frame()
	if *calls != 0 {
		t.Fatal("an empty table still opened the save dialog")
	}
	if !tt.HasText("没有可导出的行") {
		t.Fatalf("an empty export says nothing; texts = %v", tt.Texts())
	}
}

func TestLogcatPageDoesNotRetryAFailedStart(t *testing.T) {
	runner := &logcatRunner{startErr: core.Error("adb 不可用")}
	vm := newBlockingViewModel(t, runner)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Disconnect(context.Background(), profile.ID) })
	vm.OpenLogcat()
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	waitForText(t, tt, "日志流无法启动：adb 不可用")
	if got := runner.attempts(); got != 1 {
		t.Fatalf("started %d streams, want one attempt", got)
	}
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if got := runner.attempts(); got != 1 {
		t.Fatalf("a failed start was retried %d times; the user did not ask for that", got)
	}
	if err := tt.Click("重新开始"); err != nil {
		t.Fatalf("Click(重新开始) error: %v", err)
	}
	tt.Frame()
	if got := runner.attempts(); got != 2 {
		t.Fatalf("restarting made %d attempts, want a second one", got)
	}
}

func TestCloseLogcatStopsAHiddenWindowStream(t *testing.T) {
	vm, _ := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	stream := vm.Logcat.stream
	if stream == nil {
		t.Fatal("the page never started its stream")
	}
	// Closing the window hides it, and a hidden window draws no frames: the
	// page cannot notice it was left, so the stream has to be stopped here.
	vm.CloseLogcat()
	if !stream.Closed() {
		t.Fatal("the stream kept reading the device while the window was hidden")
	}
}

func TestLogcatRowCopiesItsLine(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1234  1250 E AndroidRuntime: boom\n" +
		"11-18 09:30:01.123  1234  1250 E AndroidRuntime: \tat com.demo.Main.run(Main.java:1)\n")
	waitForText(t, tt, "boom")

	copied := ""
	original := writeClipboard
	t.Cleanup(func() { writeClipboard = original })
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	if err := tt.Click("复制这一行"); err != nil {
		t.Fatalf("Click(复制这一行) error: %v", err)
	}
	year := itoa(time.Now().Year())
	for _, want := range []string{
		year + "-11-18 09:30:01.123 E/AndroidRuntime(1234): boom",
		"\tat com.demo.Main.run(Main.java:1)",
	} {
		if !strings.Contains(copied, want) {
			t.Fatalf("the clipboard has no %q; it holds %q", want, copied)
		}
	}
}

func TestLogcatLineTextKeepsTheWholeEntry(t *testing.T) {
	entry := core.LogcatEntry{
		At: time.Date(2026, 11, 18, 9, 30, 1, 123e6, time.UTC), Level: core.LogcatFatal,
		PID: 42, TID: 43, Tag: "demo", Text: "first\n\tat frame",
	}
	want := "2026-11-18 09:30:01.123 F/demo(42): first\n\tat frame"
	if got := logcatLineText(entry); got != want {
		t.Fatalf("logcatLineText() = %q, want %q", got, want)
	}
}

func TestLogcatPageOpensWithRowsWhilePaused(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	// A pause that outlives a visit must still show what the stream already
	// holds: an empty table under 「已暂停」 reads as lost data.
	vm.Logcat = &logcatState{Follow: true, Paused: true}
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1  1 I Tag: kept while paused\n")
	waitForText(t, tt, "kept while paused")
	if !tt.HasText("已暂停") {
		t.Fatalf("the page does not say it is paused; texts = %v", tt.Texts())
	}
}

func TestLogcatFilterAppliesWhilePaused(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.100  1  1 I Alpha: alpha line\n" +
		"11-18 09:30:01.200  1  1 I Beta: beta line\n")
	waitForText(t, tt, "beta line")
	if err := tt.Click("暂停"); err != nil {
		t.Fatalf("Click(暂停) error: %v", err)
	}
	// A filter is a question about the buffer, not about the screen: it has to
	// work on a paused page too, or 暂停 makes the page unsearchable.
	vm.Logcat.Filter.Tags = "Alpha"
	tt.Frame()
	if tt.HasText("beta line") || !tt.HasText("alpha line") {
		t.Fatalf("the tag filter did nothing while paused; texts = %v", tt.Texts())
	}
	// Paused still means new lines do not arrive on their own.
	runner.feed("11-18 09:30:01.300  1  1 I Alpha: late line\n")
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if tt.HasText("late line") {
		t.Fatalf("a paused page took a new line; texts = %v", tt.Texts())
	}
	if err := tt.Click("继续"); err != nil {
		t.Fatalf("Click(继续) error: %v", err)
	}
	waitForText(t, tt, "late line")
}

func TestLogcatCellsCanBeSelectedAndCopied(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  1234  1250 I VehicleSignalHub: selectable payload text\n")
	waitForText(t, tt, "selectable payload text")

	rect, ok := tt.Find("selectable payload text")
	if !ok {
		t.Fatalf("the message is not on screen; texts = %v", tt.Texts())
	}
	// Drag across the message, the way a user copies part of a line.
	tt.SetClipboard("")
	middle := rect.Y + rect.H/2
	left, right := rect.X+2, rect.X+rect.W-2
	tt.Press(left, middle)
	tt.Move((left+right)/2, middle)
	tt.Move(right, middle)
	tt.Release(right, middle)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if got := tt.Clipboard(); !strings.Contains(got, "payload") {
		t.Fatalf("Ctrl+C after a drag copied %q, want the selected message", got)
	}
}

func TestLogcatTagCellShowsTheProcessName(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 720)
	tt.Frame()
	runner.feed("11-18 09:30:01.123  4100  4123 I VehicleSignalHub: connected\n")
	waitForText(t, tt, "connected")
	// The tag alone does not say which application logged the line; the process
	// table does, and it belongs under the tag it explains.
	waitForText(t, tt, "com.demo.camera")
}

// logcatBurst is n distinct lines, so nothing merges and the table overflows.
func logcatBurst(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(fmt.Sprintf("11-18 09:30:%02d.%03d  1  1 I Tag: line %d\n", (i/1000)%60, i%1000, i))
	}
	return b.String()
}

func TestLogcatFollowShowsTheNewestLine(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	// A window too short for the burst: the newest line is only visible if the
	// table actually follows its end.
	tt := ui.NewTester(vm.ContentView, 1100, 400)
	tt.Frame()
	runner.feed(logcatBurst(60))
	waitForText(t, tt, "line 59")
}

func TestTurningFollowBackOnJumpsToTheNewestLine(t *testing.T) {
	vm, runner := newLogcatFixture(t)
	tt := ui.NewTester(vm.ContentView, 1100, 400)
	tt.Frame()
	if err := tt.Click("跟随最新"); err != nil {
		t.Fatalf("Click(跟随最新) error: %v", err)
	}
	tt.Frame()
	runner.feed(logcatBurst(60))
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if tt.HasText("line 59") {
		t.Fatalf("a page that is not following already shows the newest line: the test proves nothing")
	}
	// Turning the switch back on has to bring the reader to the end; making
	// them scroll to the bottom first is not what the switch says.
	if err := tt.Click("跟随最新"); err != nil {
		t.Fatalf("Click(跟随最新) error: %v", err)
	}
	waitForText(t, tt, "line 59")
}
