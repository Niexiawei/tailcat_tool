//go:build darwin || linux

package core

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A task that leaves a child behind must not leave it running: the local port
// would stay busy until the child exits.
func TestKillProcessTree(t *testing.T) {
	cmd := *helperCommand("spawn-child")
	cmd.Timeout = 400 * time.Millisecond
	var mu sync.Mutex
	var out strings.Builder
	res, err := NewExecRunner().Run(context.Background(), cmd, func(s string) {
		mu.Lock()
		defer mu.Unlock()
		out.WriteString(s)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, result = %+v", res)
	}
	mu.Lock()
	text := out.String()
	mu.Unlock()
	childPID := 0
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "CHILD "); ok {
			childPID, _ = strconv.Atoi(strings.TrimSpace(rest))
		}
	}
	if childPID <= 0 {
		t.Fatalf("helper never reported its child pid: %q", text)
	}
	// Give the group kill a moment to land.
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(childPID, 0); err != nil {
			return // ESRCH: the grandchild is gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("child process %d survived the group kill", childPID)
}
