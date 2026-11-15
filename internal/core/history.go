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

// Recorder persists what would otherwise die with the process. It is optional:
// a nil Recorder keeps the app working with in-memory state only.
//
// Implementations must not block the caller for long: the manager calls them
// from its own goroutines while the UI is drawing.
type Recorder interface {
	RecordTask(TaskRecord) error
	RecordLog(LogRecord) error
	RecordReachability([]ReachabilityRecord) error
	// RecentTasks returns the newest tasks, oldest first, for the task panel
	// after a restart.
	RecentTasks(limit int) ([]Task, error)
	Close() error
}
