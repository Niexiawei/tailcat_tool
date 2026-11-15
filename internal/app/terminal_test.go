package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// readIdle collects output until the prompt comes back, so a console that
// prints nothing fails instead of blocking the suite.
func readIdle(t *testing.T, s *idleShell) string {
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
		t.Fatal("the idle console produced no output")
		return ""
	}
}

func TestIdleShellExplainsItself(t *testing.T) {
	s := newIdleShell(nil)
	defer s.Close()
	if got := readIdle(t, s); !strings.Contains(got, IdleConsoleText) {
		t.Fatalf("the idle console does not explain itself: %q", got)
	}
}

func TestIdleShellRefusesCommands(t *testing.T) {
	var toasts int
	s := newIdleShell(func() { toasts++ })
	defer s.Close()
	readIdle(t, s)

	if _, err := s.Write([]byte("ls\r")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if toasts != 1 {
		t.Fatalf("toasts = %d, want 1 after Enter", toasts)
	}
	got := readIdle(t, s)
	if !strings.Contains(got, "ls") {
		t.Fatalf("the console does not echo what was typed: %q", got)
	}
	if !strings.Contains(got, IdleConsoleText) {
		t.Fatalf("the console did not print why it refused: %q", got)
	}
	// Nothing is kept as a pending command; the line is cleared.
	if line := s.Line(); line != "" {
		t.Fatalf("the refused command was kept: %q", line)
	}
}

func TestIdleShellIgnoresUnknownControlKeys(t *testing.T) {
	s := newIdleShell(nil)
	defer s.Close()
	readIdle(t, s)
	if _, err := s.Write([]byte{0x1b, '[', 'A'}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if line := s.Line(); strings.Contains(line, "[") {
		t.Fatalf("an escape sequence was typed into the line: %q", line)
	}
}

func TestConsoleReportsAMissingTerminal(t *testing.T) {
	original := newTerminal
	newTerminal = func(terminal.Options) (*terminal.Terminal, error) {
		return nil, errors.New("no native library")
	}
	defer func() { newTerminal = original }()

	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.OpenDevice(profile.ID)
	vm.DetailTab = 1
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
	if hint := vm.consoleHint(vm.Manager.Session(profile.ID)); !strings.Contains(hint, "命令直接在") {
		t.Fatalf("hint = %q", hint)
	}
}
