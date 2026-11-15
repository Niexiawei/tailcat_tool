package core

import (
	"time"

	"github.com/google/uuid"
)

// Task is one background device operation (install, push, pull, shell, screen).
// A task is bound to the serial and adb path captured when it started, so
// switching devices in the sidebar never retargets it.
type Task struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	DeviceName string
	Serial     string
	Title      string
	Started    time.Time
	State      TaskState
	Output     string
}
