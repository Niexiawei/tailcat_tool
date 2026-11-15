package app

import (
	"io"
	"sync"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// newTerminal makes an embedded console. It is a variable so tests never load
// the native terminal library.
var newTerminal = terminal.New

// IdleConsoleText is what an idle console says; tests assert it.
const IdleConsoleText = "设备未连接"

// consoleHost is the embedded console of one device in one transport state.
type consoleHost struct {
	term *terminal.Terminal
	key  string
	// idle is set when the console talks to an idleShell instead of adb.
	idle *idleShell
}

// consoleKey says what the console would be attached to; a change of key means
// the old console must go.
func consoleKey(session core.Session) string {
	if session.State == core.StateOnline && session.Serial != "" {
		return "adb:" + session.Serial
	}
	return "idle"
}

// consoleView renders the embedded terminal of a device. A connected device
// gets a real `adb shell`; an unconnected one gets a console that explains
// itself and refuses commands.
func (vm *ViewModel) consoleView(c *ui.Context, profile core.Profile, session core.Session) {
	if vm.consoles == nil {
		vm.consoles = map[uuid.UUID]*consoleHost{}
	}
	key := consoleKey(session)
	host := vm.consoles[profile.ID]
	if host == nil || host.key != key {
		if host != nil && host.term != nil {
			_ = host.term.Close()
		}
		created, err := vm.newConsole(session, key)
		if err != nil {
			delete(vm.consoles, profile.ID)
			components.Text(c, "终端不可用："+err.Error()).FontSize(11)
			return
		}
		host = created
		vm.consoles[profile.ID] = host
	}
	ui.Column(c).Fill().Gap(6).Children(func() {
		components.Text(c, vm.consoleHint(session)).FontSize(11).TextColor(c.Theme().TextMuted)
		terminal.View(c, host.term).FillWidth().Grow(1).MinHeight(300).AutoFocus()
	})
}

// consoleHint says what the console is attached to.
func (vm *ViewModel) consoleHint(session core.Session) string {
	if session.State == core.StateOnline && session.Serial != "" {
		return "已连接 " + session.Serial + " · 命令直接在该设备的 shell 中执行"
	}
	return "设备未连接 · 可在此查看提示，输入的命令不会执行"
}

// newConsole builds the console for one transport state.
func (vm *ViewModel) newConsole(session core.Session, key string) (*consoleHost, error) {
	// The demo never runs real commands, so it gets the idle console too.
	if key != "idle" && !vm.Demo {
		if adbPath := vm.Manager.Tools().FindToolPath(core.ToolADB, vm.Locate); adbPath != "" {
			term, err := newTerminal(terminal.Options{
				Command: []string{adbPath, "-s", session.Serial, "shell"},
			})
			if err == nil {
				return &consoleHost{term: term, key: key}, nil
			}
		}
	}
	// No connection, or no adb to drive it: an idle console.
	idle := newIdleShell(func() {
		vm.FailText(IdleConsoleText)
	})
	term, err := newTerminal(terminal.Options{Conn: idle})
	if err != nil {
		return nil, err
	}
	return &consoleHost{term: term, key: key, idle: idle}, nil
}

// CloseConsoles closes every embedded console.
func (vm *ViewModel) CloseConsoles() {
	for id, host := range vm.consoles {
		if host.term != nil {
			_ = host.term.Close()
		}
		if host.idle != nil {
			_ = host.idle.Close()
		}
		delete(vm.consoles, id)
	}
}

// idleShell is the console of a device that is not connected: it prints why,
// echoes what is typed, refuses every command and asks for a toast.
type idleShell struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []byte
	line   []byte
	notify func()
	closed bool
	// esc and csi track a key's escape sequence so it is not echoed as text.
	esc, csi bool
}

// newIdleShell starts an idle console with its explanation on screen.
func newIdleShell(notify func()) *idleShell {
	s := &idleShell{notify: notify}
	s.cond = sync.NewCond(&s.mu)
	s.push(IdleConsoleText + "\r\n")
	s.push("连接设备后即可在此执行 adb shell 命令。\r\n")
	s.push("\r\n$ ")
	return s
}

func (s *idleShell) push(text string) {
	s.mu.Lock()
	s.queue = append(s.queue, text...)
	s.cond.Signal()
	s.mu.Unlock()
}

// Read hands the queued output to the terminal.
func (s *idleShell) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.queue)
	s.queue = s.queue[n:]
	return n, nil
}

// Write echoes the keystrokes and refuses the command on Enter: nothing is
// sent to adb, and the user is told why with a toast.
func (s *idleShell) Write(p []byte) (int, error) {
	for _, b := range p {
		// Escape sequences (arrows, function keys) are not text.
		if s.csi {
			if b >= 0x40 && b <= 0x7e {
				s.csi = false
			}
			continue
		}
		if s.esc {
			s.esc = false
			if b == '[' || b == 'O' {
				s.csi = true
			}
			continue
		}
		switch {
		case b == 0x1b:
			s.esc = true
		case b == '\r' || b == '\n':
			s.line = nil
			s.push("\r\n" + IdleConsoleText + "\r\n$ ")
			if s.notify != nil {
				s.notify()
			}
		case b == 0x7f || b == 0x08:
			if len(s.line) > 0 {
				s.line = s.line[:len(s.line)-1]
				s.push("\b \b")
			}
		case b == 0x03:
			s.line = nil
			s.push("^C\r\n$ ")
		case b >= 0x20:
			s.line = append(s.line, b)
			s.push(string(b))
		}
	}
	return len(p), nil
}

// Close ends the idle console.
func (s *idleShell) Close() error {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

// Line is what the user typed so far, without the trailing Enter.
func (s *idleShell) Line() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.line)
}
