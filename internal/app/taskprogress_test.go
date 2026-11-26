package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

func TestBatchProgressAddsUpFinishedAndRunningFiles(t *testing.T) {
	batch := taskBatch{Kind: taskPush, Tasks: []uuid.UUID{uuid.New(), uuid.New()}}
	tasks := []core.Task{
		{ID: batch.Tasks[0], State: core.TaskSucceeded},
		{ID: batch.Tasks[1], State: core.TaskRunning, Output: "[ 50%] /sdcard/Download/b.bin"},
	}
	view := batchSnapshot(batch, tasks, nil)
	if view.Finished != 1 || view.Count != 2 {
		t.Fatalf("view = %+v", view)
	}
	if view.Fraction < 0.74 || view.Fraction > 0.76 {
		t.Fatalf("fraction = %v, want 0.75", view.Fraction)
	}
	if view.Text != "已完成 1 / 2 个文件" {
		t.Fatalf("text = %q", view.Text)
	}
}

func TestBatchProgressIsIndeterminateWithoutAPercentage(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskInstall, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskRunning, Output: "Performing Streamed Install\n"}}, nil)
	if view.Fraction != -1 {
		t.Fatalf("fraction = %v, want -1 (不确定)", view.Fraction)
	}
	if view.Text != "Performing Streamed Install" {
		t.Fatalf("text = %q, want adb's last line", view.Text)
	}
}

func TestBatchProgressFallsBackToTheBytesOnDisk(t *testing.T) {
	id := uuid.New()
	size := func(string) int64 { return 512 }
	view := batchSnapshot(taskBatch{Kind: taskPull, Tasks: []uuid.UUID{id}, LocalPath: "/tmp/a.bin", Total: 1024},
		[]core.Task{{ID: id, State: core.TaskRunning}}, size)
	if view.Fraction != 0.5 || view.Text != "已传输 512 B / 1.0 KB" {
		t.Fatalf("view = %+v", view)
	}
}

func TestBatchProgressShowsWhyTheTaskFailed(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskInstall, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskFailed, Output: "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]\n"}}, nil)
	if view.State != core.TaskFailed || !strings.Contains(view.Reason, "INSTALL_FAILED_INSUFFICIENT_STORAGE") {
		t.Fatalf("view = %+v", view)
	}
}

func TestBatchProgressIsCompleteWhenEveryTaskFinished(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskPush, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskSucceeded}}, nil)
	if view.State != core.TaskSucceeded || view.Fraction != 1 || view.Text != "已完成" {
		t.Fatalf("view = %+v", view)
	}
}

func TestElapsedLabelSpellsOutMinutes(t *testing.T) {
	cases := map[time.Duration]string{
		3 * time.Second:               "已用 3 秒",
		65 * time.Second:              "已用 1 分 05 秒",
		2*time.Minute + 7*time.Second: "已用 2 分 07 秒",
	}
	for in, want := range cases {
		if got := elapsedLabel(in); got != want {
			t.Fatalf("elapsedLabel(%v) = %q, want %q", in, got, want)
		}
	}
}
