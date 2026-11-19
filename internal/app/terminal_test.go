package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// readConsole collects output until the prompt comes back, so a console that
// prints nothing fails instead of blocking the suite.
func readConsole(t *testing.T, s *lineConsole) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		var out strings.Builder
		for out.Len() < 4096 {
			n, err := s.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
			}
			if err != nil || strings.HasSuffix(out.String(), "$ ") {
				break
			}
		}
		done <- out.String()
	}()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("the console produced no output")
		return ""
	}
}

func TestLineConsoleShowsBannerAndPrompt(t *testing.T) {
	s := newLineConsole("说明行", nil)
	defer s.Close()
	if got := readConsole(t, s); !strings.Contains(got, "说明行") || !strings.HasSuffix(got, "$ ") {
		t.Fatalf("console = %q", got)
	}
}

func TestLineConsoleHandsCompletedLines(t *testing.T) {
	var lines []string
	s := newLineConsole("", nil)
	s.onLine = func(line string) {
		lines = append(lines, line)
		s.prompt()
	}
	defer s.Close()
	readConsole(t, s)

	if _, err := s.Write([]byte("shell ls\r")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if len(lines) != 1 || lines[0] != "shell ls" {
		t.Fatalf("lines = %q", lines)
	}
	if got := readConsole(t, s); !strings.Contains(got, "shell ls") {
		t.Fatalf("the console does not echo what was typed: %q", got)
	}
	if line := s.Line(); line != "" {
		t.Fatalf("the line was not cleared: %q", line)
	}
}

func TestLineConsoleIgnoresEscapeSequences(t *testing.T) {
	s := newLineConsole("", nil)
	defer s.Close()
	readConsole(t, s)
	if _, err := s.Write([]byte{0x1b, '[', 'A'}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if line := s.Line(); line != "" {
		t.Fatalf("an escape sequence was typed into the line: %q", line)
	}
}

func TestIdleConsoleRefusesCommands(t *testing.T) {
	vm := newTestViewModel(t)
	console := newLineConsole(IdleConsoleText, nil)
	console.onLine = vm.idleLineHandler(console)
	defer console.Close()
	readConsole(t, console)

	if _, err := console.Write([]byte("ls\r")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got := readConsole(t, console)
	if !strings.Contains(got, IdleConsoleText) {
		t.Fatalf("the console did not print why it refused: %q", got)
	}
	queued := vm.drainToasts()
	if len(queued) != 1 || queued[0].Type != components.ToastError {
		t.Fatalf("toasts = %+v", queued)
	}
}

func TestADBConsoleRunsCommands(t *testing.T) {
	vm, calls := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		for _, arg := range c.Args {
			if arg == "get-state" {
				return core.Result{Output: "device\n"}, nil
			}
		}
		return core.Result{Output: "attached: /sdcard/a.mp4\n"}, nil
	})
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)

	console := newLineConsole("", nil)
	console.onLine = func(line string) {
		vm.runADBLine(profile, vm.Manager.Session(profile.ID), console, line)
	}
	defer console.Close()
	readConsole(t, console)

	if _, err := console.Write([]byte("pull /sdcard/a.mp4 .\r")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got := readConsole(t, console)
	if !strings.Contains(got, "/sdcard/a.mp4") {
		t.Fatalf("the command output missing: %q", got)
	}
	ran := calls()
	if len(ran) == 0 {
		t.Fatal("no adb command was run")
	}
	last := ran[len(ran)-1]
	if !strings.Contains(last, "pull") {
		t.Fatalf("the last adb call was %q", last)
	}
}

func TestConsoleFallsBackWhenTerminalIsUnavailable(t *testing.T) {
	original := newTerminal
	newTerminal = func(terminal.Options) (*terminal.Terminal, error) {
		return nil, errors.New("no native library")
	}
	defer func() { newTerminal = original }()

	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.OpenDevice(profile.ID)
	vm.DetailTab = detailTerminal
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("终端不可用：no native library") {
		t.Fatalf("no message about an unusable console; texts = %v", tt.Texts())
	}
}

func TestConsoleHintTracksTheConnection(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if hint := vm.consoleHint(vm.Manager.Session(profile.ID)); !strings.Contains(hint, "设备未连接") {
		t.Fatalf("hint = %q", hint)
	}
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	if hint := vm.consoleHint(vm.Manager.Session(profile.ID)); !strings.Contains(hint, "命令直接") {
		t.Fatalf("hint = %q", hint)
	}
	vm.ConsoleTab = ConsoleADB
	if hint := vm.consoleHint(vm.Manager.Session(profile.ID)); !strings.Contains(hint, "adb 子命令") {
		t.Fatalf("adb hint = %q", hint)
	}
}

func TestConsoleKeyFollowsModeAndState(t *testing.T) {
	online := core.Session{State: core.StateOnline, Serial: "127.0.0.1:5555"}
	if got := consoleKey(online, ConsoleShell); got != "adb:127.0.0.1:5555:shell" {
		t.Fatalf("key = %q", got)
	}
	if got := consoleKey(online, ConsoleADB); got != "adb:127.0.0.1:5555:cmd" {
		t.Fatalf("key = %q", got)
	}
	offline := core.Session{State: core.StateFailed, Serial: "127.0.0.1:5555"}
	if got := consoleKey(offline, ConsoleShell); got != "idle" {
		t.Fatalf("key = %q, want idle", got)
	}
}

func TestADBConsoleErrorPathCompletes(t *testing.T) {
	vm, _ := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		for _, arg := range c.Args {
			if arg == "get-state" {
				return core.Result{Output: "device\n"}, nil
			}
		}
		return core.Result{ExitCode: 1, Output: "adb.exe: unknown command cat"}, nil
	})
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)

	console := newLineConsole("", nil)
	console.onLine = func(line string) {
		vm.runADBLine(profile, vm.Manager.Session(profile.ID), console, line)
	}
	defer console.Close()
	readConsole(t, console)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = console.Write([]byte("cat /proc/meminfo\r"))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a failing adb command hung the console")
	}
	if got := readConsole(t, console); !strings.Contains(got, "unknown command") {
		t.Fatalf("the adb error was not shown: %q", got)
	}
	queued := vm.drainToasts()
	if len(queued) != 1 || queued[0].Type != components.ToastError {
		t.Fatalf("toasts = %+v", queued)
	}
}

func TestConsoleThemeIsAlwaysDark(t *testing.T) {
	theme := consoleTheme()
	if theme.Background != ui.Hex("#000000") {
		t.Fatalf("console background = %v, want black", theme.Background)
	}
	if theme.Foreground != ui.Hex("#f2f2f2") {
		t.Fatalf("console foreground = %v, want near-white", theme.Foreground)
	}
}

func TestLeavingTheTerminalTabDoesNotWaitForItsProcess(t *testing.T) {
	release := make(chan struct{})
	original := closeConsole
	t.Cleanup(func() { closeConsole = original })
	closeConsole = func(*consoleHost) { <-release }

	vm := newTestViewModel(t)
	id := uuid.New()
	vm.consoles = map[uuid.UUID]*consoleHost{id: {key: "adb:shell"}}

	done := make(chan struct{})
	go func() {
		vm.dropConsole(id)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dropConsole waited for the console's process: stopping an adb shell costs most of a second on Windows")
	}
	close(release)
}

func TestQuittingTheAppWaitsForTheConsoles(t *testing.T) {
	stopped := make(chan struct{}, 1)
	original := closeConsole
	t.Cleanup(func() { closeConsole = original })
	closeConsole = func(*consoleHost) { stopped <- struct{}{} }

	vm := newTestViewModel(t)
	vm.consoles = map[uuid.UUID]*consoleHost{uuid.New(): {key: "adb:shell"}}
	vm.CloseConsoles()
	select {
	case <-stopped:
	default:
		t.Fatal("CloseConsoles returned before the console was stopped: quitting would leave an adb shell behind")
	}
}
