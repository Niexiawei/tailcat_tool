package core

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Niexiawei/tailcat-tool/internal/platform"
)

// maxCapturedOutput is how much of a command's output Run keeps in Result.
const maxCapturedOutput = 1 << 20

// stopGracePeriod is how long Stop waits after a terminating signal before it
// kills the process tree.
const stopGracePeriod = 2 * time.Second

// Proc is a running process.
type Proc struct {
	// PID is the process identifier.
	PID int
	// Done receives the exit code once, when the process is reaped.
	Done <-chan int

	sink    *outputSink
	stopOne sync.Once
}

// Stop terminates the process (and, on Unix, its process group). It is safe to
// call more than once.
func (p *Proc) Stop() { p.stopOne.Do(p.forceStop) }

func (p *Proc) forceStop() {
	if p.PID <= 0 {
		return
	}
	platform.Terminate(p.PID)
	go func() {
		deadline := time.Now().Add(stopGracePeriod)
		for time.Now().Before(deadline) {
			if !platform.Alive(p.PID) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		platform.Kill(p.PID)
	}()
}

// Output returns the captured output so far.
func (p *Proc) Output() string {
	if p.sink == nil {
		return ""
	}
	return p.sink.capturedOutput()
}

// ExecRunner runs real processes.
type ExecRunner struct{}

// NewExecRunner returns a runner backed by the operating system.
func NewExecRunner() *ExecRunner { return &ExecRunner{} }

// Start launches the command.
func (r *ExecRunner) Start(_ context.Context, c Command, onOutput func(string)) (*Proc, error) {
	if c.Path == "" {
		return nil, Error("未指定要执行的命令")
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	platform.ConfigureProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Errorf("无法读取命令输出：%v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, Errorf("无法读取命令输出：%v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, Errorf("无法运行 %s：%v", filepath.Base(c.Path), err)
	}

	sink := newOutputSink(onOutput)
	proc := &Proc{PID: cmd.Process.Pid, sink: sink}
	done := make(chan int, 1)
	proc.Done = done

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		drain(stdout, sink)
	}()
	go func() {
		defer readers.Done()
		drain(stderr, sink)
	}()

	go func() {
		// Wait must run only after the pipes are drained.
		readers.Wait()
		waitErr := cmd.Wait()
		sink.flush()
		done <- exitCodeOf(waitErr)
	}()
	return proc, nil
}

// Run starts the command and waits for it.
func (r *ExecRunner) Run(ctx context.Context, c Command, onOutput func(string)) (Result, error) {
	proc, err := r.Start(ctx, c, onOutput)
	if err != nil {
		return Result{}, err
	}

	var timeout <-chan time.Time
	if c.Timeout > 0 {
		timer := time.NewTimer(c.Timeout)
		defer timer.Stop()
		timeout = timer.C
	}

	var res Result
	select {
	case code := <-proc.Done:
		res.ExitCode = code
	case <-timeout:
		res.TimedOut = true
		proc.Stop()
		res.ExitCode = <-proc.Done
	case <-ctx.Done():
		res.Cancelled = true
		proc.Stop()
		res.ExitCode = <-proc.Done
	}
	res.Output = proc.Output()
	return res, nil
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func drain(f io.ReadCloser, sink *outputSink) {
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			sink.write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// outputSink merges both pipes, emits only whole UTF-8 chunks, and keeps the
// tail of everything it saw.
type outputSink struct {
	mu       sync.Mutex
	pending  []byte
	captured []byte
	emit     func(string)
}

func newOutputSink(emit func(string)) *outputSink {
	return &outputSink{emit: emit}
}

func (s *outputSink) write(p []byte) {
	s.mu.Lock()
	s.pending = append(s.pending, p...)
	cut := completeUTF8Prefix(s.pending)
	var text string
	if cut > 0 {
		text = string(s.pending[:cut])
		s.pending = append(s.pending[:0], s.pending[cut:]...)
		s.appendCaptured(text)
	}
	emit := s.emit
	s.mu.Unlock()
	if text != "" && emit != nil {
		emit(text)
	}
}

// flush delivers whatever bytes are left, even if they never formed a complete
// rune, so nothing the process printed is silently dropped.
func (s *outputSink) flush() {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	text := string(s.pending)
	s.pending = s.pending[:0]
	s.appendCaptured(text)
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(text)
	}
}

func (s *outputSink) appendCaptured(text string) {
	s.captured = append(s.captured, text...)
	if len(s.captured) > maxCapturedOutput {
		s.captured = append(s.captured[:0], s.captured[len(s.captured)-maxCapturedOutput:]...)
	}
}

func (s *outputSink) capturedOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.captured)
}

// completeUTF8Prefix returns the length of the longest prefix of b that ends on
// a rune boundary, so a multi-byte character split across two reads is never
// emitted as a broken string.
func completeUTF8Prefix(b []byte) int {
	n := len(b)
	if n == 0 {
		return 0
	}
	start := n - 1
	for back := 1; back <= utf8.UTFMax && start >= 0; back++ {
		if b[start]&0xC0 != 0x80 { // not a continuation byte
			break
		}
		start--
	}
	if start < 0 {
		return 0
	}
	if !utf8.FullRune(b[start:]) {
		return start
	}
	return n
}
