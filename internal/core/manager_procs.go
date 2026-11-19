package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// processTableTimeout bounds one `ps` round trip: the table is a few hundred
// lines and the answer is only a convenience for the log page.
const processTableTimeout = 6 * time.Second

// RefreshProcessNames reads the device's process table once, so the log page can
// say which process a PID belonged to.
//
// It is a silent read, not a task: no timeout-heavy machinery, no history row —
// the same shape as the file page's directory reads.
func (m *Manager) RefreshProcessNames(ctx context.Context, id uuid.UUID) error {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return err
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", "ps -A"},
		Timeout: processTableTimeout,
	}, nil)
	if err != nil {
		return sanitizeError(err)
	}
	if res.ExitCode != 0 {
		return sanitizeError(Error(strings.TrimSpace(Redact(res.Output, ""))))
	}
	if !m.targetIsCurrent(id, target) {
		return ErrCancelled
	}
	names := parseProcessTable(res.Output)
	m.mu.Lock()
	m.processNames[id] = names
	m.mu.Unlock()
	return nil
}

// ProcessName is the name a PID had when the table was last read, or "" when it
// is not known. A PID can be recycled, so an old reading is a hint, not a fact.
func (m *Manager) ProcessName(id uuid.UUID, pid int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processNames[id][pid]
}

// forgetProcessNamesLocked drops a device's table with its channel.
func (m *Manager) forgetProcessNamesLocked(id uuid.UUID) {
	delete(m.processNames, id)
}

// parseProcessTable reads `ps -A`: only the PID column and the last one matter,
// and every Android release pads the middle differently.
func parseProcessTable(output string) map[int]string {
	names := map[int]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			// The header, or a line this build does not understand.
			continue
		}
		if name := fields[len(fields)-1]; name != "" {
			names[pid] = name
		}
	}
	return names
}
