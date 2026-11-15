package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// The runner tests re-execute this test binary as the child process, so they
// exercise real pipes, real exits and real signals on every platform.
const helperEnv = "CORE_RUNNER_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(runHelper(mode))
	}
	os.Exit(m.Run())
}

func runHelper(mode string) int {
	switch mode {
	case "both-pipes":
		fmt.Fprint(os.Stdout, "out-1\n")
		fmt.Fprint(os.Stderr, "err-1\n")
		fmt.Fprint(os.Stdout, "out-2\n")
		fmt.Fprint(os.Stderr, "err-2\n")
		return 0
	case "utf8-split":
		// "世界" split in the middle of the first rune, then the rest.
		data := []byte("世界\n")
		os.Stdout.Write(data[:2])
		time.Sleep(30 * time.Millisecond)
		os.Stdout.Write(data[2:])
		return 0
	case "big-output":
		chunk := strings.Repeat("a", 64*1024)
		for i := 0; i < 24; i++ { // 1.5 MiB
			if _, err := os.Stdout.WriteString(chunk); err != nil {
				return 3
			}
		}
		fmt.Fprint(os.Stdout, "TAIL-END")
		return 0
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	case "exit-code":
		return 7
	case "spawn-child":
		// Start a long-lived grandchild and report its pid, then idle.
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "child start failed: %v\n", err)
			return 4
		}
		fmt.Fprintf(os.Stdout, "CHILD %d\n", cmd.Process.Pid)
		time.Sleep(30 * time.Second)
		return 0
	}
	return 2
}

// helperCommand builds a command that runs this test binary in the given mode.
func helperCommand(mode string) *Command {
	return &Command{Path: os.Args[0], Args: []string{"-test.run=^$"}, Env: []string{helperEnv + "=" + mode}}
}

func TestRunCapturesBothPipes(t *testing.T) {
	var mu sync.Mutex
	var chunks []string
	res, err := NewExecRunner().Run(context.Background(), *helperCommand("both-pipes"), func(s string) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, s)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{"out-1", "err-1", "out-2", "err-2"} {
		if !strings.Contains(res.Output, want) && !strings.Contains(strings.Join(chunks, ""), want) {
			t.Fatalf("missing %q in output %q / chunks %v", want, res.Output, chunks)
		}
	}
}

func TestRunExitCode(t *testing.T) {
	res, err := NewExecRunner().Run(context.Background(), *helperCommand("exit-code"), nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
	if err := res.RequireSuccess(); err == nil {
		t.Fatal("RequireSuccess() accepted a non-zero exit code")
	}
}

func TestRunTimeout(t *testing.T) {
	cmd := *helperCommand("sleep")
	cmd.Timeout = 200 * time.Millisecond
	start := time.Now()
	res, err := NewExecRunner().Run(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, result = %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took %v; the process was not stopped", elapsed)
	}
	if res.Cancelled {
		t.Fatalf("a timeout must not be reported as cancellation: %+v", res)
	}
	if err := res.RequireSuccess(); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("RequireSuccess() = %v, want a timeout error", err)
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	res, err := NewExecRunner().Run(ctx, *helperCommand("sleep"), nil)
	if err != nil && err != ErrCancelled {
		t.Fatalf("Run() error: %v", err)
	}
	if !res.Cancelled {
		t.Fatalf("Cancelled = false, result = %+v", res)
	}
	if res.TimedOut {
		t.Fatalf("a cancellation must not be reported as a timeout: %+v", res)
	}
}

func TestRunOutputTailCap(t *testing.T) {
	res, err := NewExecRunner().Run(context.Background(), *helperCommand("big-output"), nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(res.Output) > maxCapturedOutput {
		t.Fatalf("captured %d bytes, want at most %d", len(res.Output), maxCapturedOutput)
	}
	if !strings.HasSuffix(res.Output, "TAIL-END") {
		t.Fatalf("captured output lost its tail: ...%q", tailString(res.Output, 40))
	}
}

func TestUTF8SplitChunk(t *testing.T) {
	var mu sync.Mutex
	var got []string
	res, err := NewExecRunner().Run(context.Background(), *helperCommand("utf8-split"), func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	for _, chunk := range got {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %q is not valid UTF-8", chunk)
		}
	}
	if joined := strings.Join(got, ""); strings.ContainsRune(joined, utf8.RuneError) {
		t.Fatalf("streamed text contains a replacement character: %q", joined)
	}
	if !strings.Contains(res.Output, "世界") || strings.ContainsRune(res.Output, utf8.RuneError) {
		t.Fatalf("Output = %q, want 世界 without replacement characters", res.Output)
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestProcStopIsIdempotent(t *testing.T) {
	proc, err := NewExecRunner().Start(context.Background(), *helperCommand("sleep"), nil)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if proc.PID <= 0 {
		t.Fatalf("PID = %d", proc.PID)
	}
	proc.Stop()
	proc.Stop()
	select {
	case <-proc.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after Stop()")
	}
}

func TestStartErrorForMissingBinary(t *testing.T) {
	_, err := NewExecRunner().Start(context.Background(), Command{Path: "/definitely/not/here/adb"}, nil)
	if err == nil {
		t.Fatal("Start() accepted a missing executable")
	}
}

func TestEnvOverridesParent(t *testing.T) {
	cmd := Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^$"},
		Env:  []string{helperEnv + "=exit-code"},
	}
	res, err := NewExecRunner().Run(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7 (env override did not reach the child)", res.ExitCode)
	}
	if strconv.Itoa(res.ExitCode) == "" {
		t.Fatal("unreachable")
	}
}
