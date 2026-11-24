package core

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// DeviceUser is one Android user profile on the device: the number
// `adb install --user` takes, and the label the device gives it.
type DeviceUser struct {
	ID   int
	Name string
	// Current is the user the device said is running now. It is a marker, not
	// a ranking: a device can report none.
	Current bool
	// Running is pm's own marker for a started profile. A profile that is not
	// running still accepts an install, so the views do not gate on it.
	Running bool
}

// deviceUsersScript asks for every profile and which one is running now in one
// round trip: `pm list users` alone does not say which one is current.
const deviceUsersScript = "pm list users; echo @@CURRENT; am get-current-user"

// ParseDeviceUsers reads `pm list users` output. Anything that is not a
// UserInfo record is skipped: some devices answer with a SecurityException
// instead of a list, and a half-printed line is not an entry.
func ParseDeviceUsers(output string, current int) []DeviceUser {
	users := make([]DeviceUser, 0, 2)
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		start := strings.Index(line, "UserInfo{")
		closer := "}"
		if start < 0 {
			start = strings.Index(line, "UserInfo(")
			closer = ")"
		}
		if start < 0 {
			continue
		}
		record := line[start+len("UserInfo"):]
		end := strings.LastIndex(record, closer)
		if end < 0 {
			continue
		}
		inner := record[1:end]
		fields := strings.SplitN(inner, ":", 3)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name == "" {
			// The number is what adb takes; an unnamed profile still has to be
			// offered rather than shown as blank.
			name = strconv.Itoa(id)
		}
		running := strings.Contains(strings.TrimSpace(record[end+1:]), "running")
		users = append(users, DeviceUser{ID: id, Name: name, Current: id == current, Running: running})
	}
	return users
}

// ParseCurrentUser reads what `am get-current-user` printed, if it printed a
// number at all: an old device or a locked-down build answers with an error.
func ParseCurrentUser(output string) (int, bool) {
	text := strings.TrimSpace(output)
	id, err := strconv.Atoi(text)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

// DeviceUsers reads the device's profiles and which one is running now. The
// install dialog asks for it when it opens, because a profile can be added
// between connecting and installing; the overview reads Session.Users instead.
func (m *Manager) DeviceUsers(ctx context.Context, id uuid.UUID) ([]DeviceUser, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return nil, err
	}
	users, err := m.readDeviceUsers(ctx, target)
	if err != nil {
		return nil, err
	}
	if !m.targetIsCurrent(id, target) {
		return nil, ErrCancelled
	}
	return users, nil
}

// readDeviceUsers runs the one shell that answers both questions.
func (m *Manager) readDeviceUsers(ctx context.Context, target operationTarget) ([]DeviceUser, error) {
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", deviceUsersScript},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil {
		return nil, sanitizeError(err)
	}
	if res.ExitCode != 0 {
		return nil, Error(MsgUsersUnreadable)
	}
	output := strings.ReplaceAll(res.Output, "\r", "")
	list, currentText, _ := strings.Cut(output, "@@CURRENT")
	current, ok := ParseCurrentUser(currentText)
	if !ok {
		// An older device may not know am get-current-user: show every profile,
		// none of them marked, rather than pretending one is current.
		current = -1
	}
	users := ParseDeviceUsers(list, current)
	if len(users) == 0 {
		return nil, Error(MsgUsersUnreadable)
	}
	return users, nil
}

// storeDeviceUsers publishes what the refresh read, so the overview shows it
// without a second query.
func (m *Manager) storeDeviceUsers(id uuid.UUID, users []DeviceUser) {
	m.mu.Lock()
	m.setStateLocked(id, func(s *Session) { s.Users = users })
	m.notify()
	m.mu.Unlock()
}
