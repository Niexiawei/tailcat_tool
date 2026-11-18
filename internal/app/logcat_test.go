package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
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
}

func (r *logcatRunner) Start(_ context.Context, c core.Command, onOutput func(string)) (*core.Proc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan int, 1)
	proc := &core.Proc{Done: done}
	r.starts = append(r.starts, c)
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
	return core.Result{}, nil
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
	waitForText(t, tt, "已接收 1 行 · 显示 1 行")

	// One line more than the ring holds: the page has to say what it lost.
	burst := strings.Builder{}
	for i := 0; i < core.LogcatMaxLines+1; i++ {
		burst.WriteString("11-18 09:30:01.123  1  1 I Tag: line\n")
	}
	runner.feed(burst.String())
	waitForText(t, tt, "丢弃 2 行")
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
