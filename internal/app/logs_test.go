package app

import (
	"context"
	"math"
	"strings"
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
	vm.DetailTab = 2
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
	vm.DetailTab = 2
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
