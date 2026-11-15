package core

import (
	"context"
	"strings"
	"time"
)

// Command is one external process invocation: an absolute path, its arguments,
// extra environment entries and an optional timeout.
type Command struct {
	Path    string
	Args    []string
	Env     []string
	Timeout time.Duration
}

// Result is the outcome of a finished command. Output holds at most the last
// maxCapturedOutput bytes so a runaway task cannot grow the heap.
type Result struct {
	ExitCode  int
	Output    string
	TimedOut  bool
	Cancelled bool
}

// RequireSuccess turns a finished-but-unsuccessful result into an error the UI
// can show. Cancelled wins over everything else, then timeout, then exit code.
func (r Result) RequireSuccess() error {
	if r.Cancelled {
		return ErrCancelled
	}
	if r.TimedOut {
		return Error("命令超时，已停止本次操作")
	}
	if r.ExitCode != 0 {
		if out := strings.TrimSpace(r.Output); out != "" {
			return Error(out)
		}
		return Errorf("命令退出码 %d", r.ExitCode)
	}
	return nil
}

// Runner starts and runs external processes. It is an interface so every
// higher layer can be tested without spawning anything.
type Runner interface {
	// Start launches the command and returns a handle. Output from stdout and
	// stderr is merged and delivered in whole UTF-8 chunks.
	Start(ctx context.Context, c Command, onOutput func(string)) (*Proc, error)
	// Run starts the command and waits for it, applying Timeout and ctx.
	Run(ctx context.Context, c Command, onOutput func(string)) (Result, error)
}
