package core

import (
	"time"

	"github.com/google/uuid"
)

// TaskRecord is one finished or running task, as history keeps it.
type TaskRecord struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	DeviceName string
	Serial     string
	Title      string
	Started    time.Time
	Ended      time.Time
	State      TaskState
	Output     string
}

// LogRecord is one channel log line.
type LogRecord struct {
	DeviceID uuid.UUID
	At       time.Time
	Text     string
}

// ReachabilityRecord is one row of one batch test.
type ReachabilityRecord struct {
	DeviceID  uuid.UUID
	Name      string
	State     string
	Latency   string
	Route     string
	Detail    string
	CheckedAt time.Time
}

// How long and how many log lines history keeps. The page shows at most
// DefaultLogLimit rows per query; the file keeps MaxLogRows inside the last
// LogRetention window, so it cannot grow without bound.
const (
	DefaultLogLimit = 500
	MaxLogRows      = 50000
	LogRetention    = 30 * 24 * time.Hour
)

// LogLevel is how much attention one log line deserves.
type LogLevel string

// The log levels, from loudest to quietest.
const (
	LogError LogLevel = "error"
	LogWarn  LogLevel = "warn"
	LogInfo  LogLevel = "info"
)

// LogSource says who wrote a timeline row.
type LogSource string

// The three writers that share one timeline.
const (
	// LogApp is the application itself: a failed action, a startup problem.
	LogApp LogSource = "app"
	// LogChannel is one device's Tailcat channel.
	LogChannel LogSource = "channel"
	// LogTask is one background device task; it carries a TaskID.
	LogTask LogSource = "task"
)

// AppLogRecord is one line the app itself produced: a failed action, a startup
// problem, a warning. Device output is LogRecord, which carries a device.
type AppLogRecord struct {
	At    time.Time
	Level LogLevel
	Text  string
}

// TimelineEntry is one row of the log page: an app line, a channel line or a
// task, merged by time.
type TimelineEntry struct {
	At         time.Time
	Level      LogLevel
	Source     LogSource
	DeviceID   uuid.UUID // uuid.Nil for app lines
	DeviceName string
	Text       string
	TaskID     uuid.UUID // set for LogTask; opens its output
}

// LogQuery selects a slice of the timeline. DeviceID nil means every device
// and every app line; *uuid.Nil means app lines only. Level empty means every
// level, Since zero means every time, and Search holds space-separated terms
// that must all match. Limit is capped by DefaultLogLimit when zero.
type LogQuery struct {
	DeviceID *uuid.UUID
	Level    LogLevel
	Since    time.Time
	Search   string
	Limit    int
}

// HistoryReader is what the log page needs from history. The page never holds
// the store itself: internal/app must not know SQL exists.
type HistoryReader interface {
	Timeline(LogQuery) ([]TimelineEntry, error)
	// TaskOutput returns the recorded output of one task, for the dialog a
	// task row opens.
	TaskOutput(uuid.UUID) (string, error)
	// ClearLogs deletes the app and channel lines inside the query's device and
	// time range, and returns how many went away. Task history is never
	// touched: a task's output is what a user digs a failure out of.
	ClearLogs(LogQuery) (int64, error)
}

// Recorder persists what would otherwise die with the process. It is optional:
// a nil Recorder keeps the app working with in-memory state only.
//
// Implementations must not block the caller for long: the manager calls them
// from its own goroutines while the UI is drawing.
type Recorder interface {
	RecordTask(TaskRecord) error
	RecordLog(LogRecord) error
	RecordAppLog(AppLogRecord) error
	RecordReachability([]ReachabilityRecord) error
	// RecentTasks returns the newest tasks, oldest first, for the task panel
	// after a restart.
	RecentTasks(limit int) ([]Task, error)
	Close() error
}

// Label returns the Chinese label shown in the level column and the tabs.
func (l LogLevel) Label() string {
	switch l {
	case LogError:
		return "错误"
	case LogWarn:
		return "警告"
	case LogInfo:
		return "信息"
	default:
		return string(l)
	}
}

// Label returns the Chinese label of the source column.
func (s LogSource) Label() string {
	switch s {
	case LogApp:
		return "应用"
	case LogChannel:
		return "通道"
	case LogTask:
		return "任务"
	default:
		return string(s)
	}
}
