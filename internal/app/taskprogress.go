package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// taskKind says which adb command the batch runs; it decides what "progress"
// means. Only push and pull print percentages.
type taskKind int

const (
	taskInstall taskKind = iota
	taskPush
	taskPull
)

// taskBatch is the operation one dialog is running: one task, or the several
// uploads that one pick started.
type taskBatch struct {
	Device uuid.UUID
	// Title is the operation's name, e.g. 上传 update.zip / 上传 3 个文件 /
	// 安装 app-release.apk.
	Title string
	// Note is the secondary line, e.g. 安装到用户 0 · Owner（当前）.
	Note  string
	Kind  taskKind
	Tasks []uuid.UUID
	// LocalPath and Total are the pull fallback: adb does not always print a
	// percentage, so the bytes on disk answer instead.
	LocalPath string
	Total     int64
	Started   time.Time
}

// batchView is what the progress block shows for one frame.
type batchView struct {
	State    core.TaskState
	Fraction float64 // 0..1, or -1 when nothing is known yet
	Text     string
	Reason   string
	Finished int
	Count    int
}

// batchSnapshot turns the batch's tasks into what the dialog shows. A task that
// is no longer in the list (trimmed history) counts as finished: the batch did
// move past it.
func batchSnapshot(batch taskBatch, tasks []core.Task, size func(string) int64) batchView {
	byID := make(map[uuid.UUID]core.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	view := batchView{State: core.TaskSucceeded, Count: len(batch.Tasks), Fraction: 1}
	var running core.Task
	runningFound := false
	failed := false
	cancelled := false
	for _, id := range batch.Tasks {
		task, ok := byID[id]
		if !ok {
			view.Finished++
			continue
		}
		switch task.State {
		case core.TaskRunning:
			running, runningFound = task, true
		case core.TaskFailed:
			failed = true
			view.Finished++
			if reason := strings.TrimSpace(task.Output); reason != "" {
				view.Reason = reason
			}
		case core.TaskCancelled:
			cancelled = true
			view.Finished++
		default:
			view.Finished++
		}
	}
	runningFinished := view.Finished
	switch {
	case failed:
		view.State = core.TaskFailed
	case cancelled:
		view.State = core.TaskCancelled
	case runningFound:
		view.State = core.TaskRunning
	}
	if !runningFound {
		view.Text = "已完成"
		switch view.State {
		case core.TaskFailed:
			view.Text = ""
		case core.TaskCancelled:
			view.Text = "已取消"
		}
		return view
	}

	switch batch.Kind {
	case taskInstall:
		view.Fraction = -1
		if line := lastOutputLine(running.Output); line != "" {
			view.Text = line
		} else {
			view.Text = "正在安装，请稍候…"
		}
		return view
	}

	total := len(batch.Tasks)
	if percent, ok := core.ParseTransferPercent(running.Output); ok {
		view.Fraction = (float64(runningFinished) + float64(percent)/100) / float64(total)
		if total == 1 {
			view.Text = strconv.Itoa(percent) + "%"
		} else {
			view.Text = fileCountText(runningFinished, total)
		}
		return view
	}
	if total == 1 && batch.Kind == taskPull && batch.Total > 0 && batch.LocalPath != "" && size != nil {
		written := size(batch.LocalPath)
		if written < 0 {
			written = 0
		}
		if written > batch.Total {
			written = batch.Total
		}
		view.Fraction = math.Min(1, float64(written)/float64(batch.Total))
		view.Text = "已传输 " + core.FormatBytes(uint64(written)) + " / " + core.FormatBytes(uint64(batch.Total))
		return view
	}
	view.Fraction = -1
	if total > 1 {
		view.Text = fileCountText(runningFinished, total)
	}
	return view
}

// fileCountText is how a multi-file batch says where it is.
func fileCountText(finished, total int) string {
	return "已完成 " + strconv.Itoa(finished) + " / " + strconv.Itoa(total) + " 个文件"
}

// lastOutputLine is the newest thing adb said, which is all an install can
// report: adb prints no percentage for it.
func lastOutputLine(output string) string {
	lines := strings.FieldsFunc(output, func(r rune) bool { return r == '\r' || r == '\n' })
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// elapsedLabel spells out how long a task has been running.
func elapsedLabel(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return "已用 " + strconv.Itoa(seconds) + " 秒"
	}
	minutes := seconds / 60
	rest := seconds % 60
	return "已用 " + strconv.Itoa(minutes) + " 分 " + pad2(rest) + " 秒"
}

func pad2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

// progressBlock draws the bar, its text and the elapsed time, and returns what
// it drew. Both dialogs call it so the two can never disagree.
func progressBlock(c *ui.Context, batch taskBatch, tasks []core.Task, size func(string) int64) batchView {
	view := batchSnapshot(batch, tasks, size)
	components.Progress(c, view.Fraction).Label("操作进度")
	if view.Text != "" {
		components.Text(c, view.Text).FontSize(11)
	}
	if view.State == core.TaskRunning && !batch.Started.IsZero() {
		components.Text(c, elapsedLabel(time.Since(batch.Started))).FontSize(11).
			TextColor(c.Theme().TextMuted)
	}
	return view
}
