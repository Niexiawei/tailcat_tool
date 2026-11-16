package app

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// logLine is one row of the device log page.
type logLine struct {
	At   time.Time
	Text string
	// task sorts after a channel log line that happened at the same instant,
	// so a task's "已完成" line reads after the log line it caused.
	task bool
}

// deviceTimeline merges the channel log with this device's tasks, oldest
// first, so one page answers "what happened to this device".
func deviceTimeline(session core.Session, tasks []core.Task) []logLine {
	lines := make([]logLine, 0, len(session.Logs)+len(tasks))
	for _, entry := range session.Logs {
		lines = append(lines, logLine{At: entry.At, Text: entry.Text})
	}
	for _, task := range tasks {
		lines = append(lines, logLine{
			At:   task.Started,
			Text: task.Title + " · " + task.State.Label(),
			task: true,
		})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].At.Equal(lines[j].At) {
			return !lines[i].task && lines[j].task
		}
		return lines[i].At.Before(lines[j].At)
	})
	return lines
}

// logText renders a timeline one entry per line, each prefixed with its
// wall-clock time: "15:04:05 通道已就绪".
func logText(lines []logLine) string {
	var builder strings.Builder
	for i, line := range lines {
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line.At.Format("15:04:05"))
		builder.WriteByte(' ')
		builder.WriteString(line.Text)
	}
	return builder.String()
}

// deviceTasks keeps the tasks that belong to one device.
func deviceTasks(tasks []core.Task, id uuid.UUID) []core.Task {
	kept := make([]core.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.DeviceID == id {
			kept = append(kept, task)
		}
	}
	return kept
}

// logState is one device's read-only log buffer plus its scroll position.
type logState struct {
	buffer *ui.TextBuffer
	scroll ui.ScrollState
	text   string // what buffer currently holds
	follow bool   // was at the tail last frame
}

// logState returns the (lazily created) state of one device's log page.
func (vm *ViewModel) logState(id uuid.UUID) *logState {
	if vm.logStates == nil {
		vm.logStates = map[uuid.UUID]*logState{}
	}
	state, ok := vm.logStates[id]
	if !ok {
		state = &logState{buffer: ui.NewTextBuffer("")}
		vm.logStates[id] = state
	}
	return state
}

// logPageText refreshes one device's buffer from a session snapshot and keeps
// the follow-the-tail decision. It is the only place that writes the buffer.
func (vm *ViewModel) logPageText(id uuid.UUID, session core.Session) {
	state := vm.logState(id)
	state.follow = state.text == "" || state.scroll.Y >= state.scroll.MaxY-4
	if text := logText(deviceTimeline(session, deviceTasks(vm.Manager.Tasks(), id))); text != state.text {
		state.text = text
		state.buffer.Set(text)
	}
	if state.follow {
		state.scroll.Y = math.MaxFloat32
	}
}

// logPage renders the log tab: a header line, the optional cancel action and
// the read-only text area.
func (vm *ViewModel) logPage(c *ui.Context, profile core.Profile, session core.Session) {
	vm.logPageText(profile.ID, session)
	state := vm.logState(profile.ID)
	if state.text == "" {
		components.Text(c, "暂无通道日志与任务").FontSize(11)
		return
	}
	// ReadOnly keeps focus, selection and copying but refuses edits; the
	// text area is its own scroll container, which the probe confirmed.
	components.TextBoxBuffer(c, state.buffer, components.TextBoxOptions{Multiline: true, ReadOnly: true}).
		FillWidth().Grow(1).Label("通道日志与任务").TrackScroll(&state.scroll)
}
