package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

func TestRecordAndReadBackAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	deviceID := uuid.New()
	taskID := uuid.New()
	started := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	if err := store.RecordTask(core.TaskRecord{
		ID: taskID, DeviceID: deviceID, DeviceName: "E245", Serial: "127.0.0.1:5556",
		Title: "上传 a", Started: started, Ended: started.Add(time.Second),
		State: core.TaskSucceeded, Output: "ok",
	}); err != nil {
		t.Fatalf("RecordTask() error: %v", err)
	}
	if err := store.RecordLog(core.LogRecord{DeviceID: deviceID, At: started, Text: "ADB 已连接"}); err != nil {
		t.Fatalf("RecordLog() error: %v", err)
	}
	if err := store.RecordReachability([]core.ReachabilityRecord{{
		DeviceID: deviceID, Name: "E245", State: "reachable", Latency: "18ms",
		Route: "直连 · 1.2.3.4:41641", CheckedAt: started,
	}}); err != nil {
		t.Fatalf("RecordReachability() error: %v", err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatalf("Flush() error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopening the history: %v", err)
	}
	defer reopened.Close()
	tasks, err := reopened.RecentTasks(10)
	if err != nil {
		t.Fatalf("RecentTasks() error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	got := tasks[0]
	if got.ID != taskID || got.Title != "上传 a" || got.State != core.TaskSucceeded || got.Output != "ok" {
		t.Fatalf("task = %+v", got)
	}
	if !got.Started.Equal(started) {
		t.Fatalf("started = %v, want %v", got.Started, started)
	}
}

func TestRecentTasksIsOldestFirst(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if err := store.RecordTask(core.TaskRecord{
			ID: uuid.New(), DeviceName: "d", Title: string(rune('a' + i)),
			Started: base.Add(time.Duration(i) * time.Minute), State: core.TaskSucceeded,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.RecentTasks(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Title != "b" || tasks[1].Title != "c" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestWritesAfterCloseAreRejected(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := store.RecordTask(core.TaskRecord{ID: uuid.New()}); err == nil {
		t.Fatal("RecordTask() accepted a write after Close()")
	}
}
