package app

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// newTerminal makes an embedded console. It is a variable so tests never load
// the native terminal library.
var newTerminal = terminal.New

// consoleTheme is the console's colors: always a dark terminal, whatever the
// window's appearance, so a device shell reads the way a terminal does.
func consoleTheme() *terminal.Theme {
	theme := terminal.DarkTheme()
	theme.Background = ui.Hex("#000000")
	theme.Foreground = ui.Hex("#f2f2f2")
	return theme
}

// IdleConsoleText is what an idle console says; tests assert it.
const IdleConsoleText = "设备未连接"

// Console modes for the 终端 tab.
const (
	// ConsoleShell is an interactive `adb shell` on the device.
	ConsoleShell = iota
	// ConsoleADB runs adb commands for the device line by line, so
	// subcommands the app has no button for stay reachable.
	ConsoleADB
)

// consoleHost is the embedded console of one device in one transport state.
type consoleHost struct {
	term *terminal.Terminal
	key  string
	// lines is the line console, when the terminal talks to a lineConsole
	// instead of a real program.
	lines *lineConsole
}

// consoleKey says what the console would be attached to; a change of key means
// the old console must go.
func consoleKey(session core.Session, mode int) string {
	if session.State != core.StateOnline || session.Serial == "" {
		return "idle"
	}
	if mode == ConsoleADB {
		return "adb:" + session.Serial + ":cmd"
	}
	return "adb:" + session.Serial + ":shell"
}

// consoleView renders the embedded terminal of a device: an adb shell, an adb
// command console, or a console that explains why it cannot run anything.
func (vm *ViewModel) consoleView(c *ui.Context, profile core.Profile, session core.Session) {
	if vm.consoles == nil {
		vm.consoles = map[uuid.UUID]*consoleHost{}
	}
	key := consoleKey(session, vm.ConsoleTab)
	host := vm.consoles[profile.ID]
	if host != nil && host.key != key {
		vm.dropConsole(profile.ID)
		host = nil
	}
	if host != nil && host.exited() {
		// The program ended (the device went away, or adb exited): a console
		// showing "[Process exited]" cannot take input, so start a new one.
		vm.dropConsole(profile.ID)
		host = nil
	}
	if host == nil {
		created, err := vm.newConsole(profile, session, key)
		if err != nil {
			components.Text(c, "终端不可用："+err.Error()).FontSize(11)
			return
		}
		host = created
		vm.consoles[profile.ID] = host
	}
	device, theme := profile.ID, c.Theme()
	ui.Column(c).Fill().Gap(6).Children(func() {
		components.Text(c, vm.consoleHint(session)).FontSize(11).TextColor(theme.TextMuted)
		// The console is its own black pane, so it gets a frame and rounded
		// corners instead of bleeding into the page: the 1 DIP padding keeps
		// the terminal's paint off the border.
		ui.Column(c).FillWidth().Grow(1).MinHeight(300).Padding(1).
			Radius(theme.Radius).Border(1, theme.Border).Clip().Children(func() {
			view := terminal.View(c, host.term).Fill().Radius(theme.Radius).AutoFocus()
			// Leaving the page closes the console: coming back starts a fresh
			// process instead of a dead one.
			view.OnUnmount(func() { vm.dropConsole(device) })
		})
	})
}

// exited reports whether the console's program has ended.
func (h *consoleHost) exited() bool {
	if h.term == nil {
		return true
	}
	select {
	case <-h.term.Done():
		return true
	default:
		return false
	}
}

// consoleHint says what the console is attached to.
func (vm *ViewModel) consoleHint(session core.Session) string {
	if session.State == core.StateOnline && session.Serial != "" {
		if vm.ConsoleTab == ConsoleADB {
			return "已连接 " + session.Serial + " · 输入 adb 子命令，如 pull /sdcard/a.mp4 .（不用写 adb 与 -s）"
		}
		return "已连接 " + session.Serial + " · 命令直接在该设备的 shell 中执行"
	}
	return "设备未连接 · 可在此查看提示，输入的命令不会执行"
}

// newConsole builds the console for one transport state and mode.
func (vm *ViewModel) newConsole(profile core.Profile, session core.Session, key string) (*consoleHost, error) {
	// The demo never runs real commands, so it gets the idle console too.
	connected := key != "idle" && !vm.Demo
	if connected && vm.ConsoleTab == ConsoleShell {
		if adbPath := vm.Manager.Tools().FindToolPath(core.ToolADB, vm.Locate); adbPath != "" {
			term, err := newTerminal(terminal.Options{
				Command: []string{adbPath, "-s", session.Serial, "shell"},
				Theme:   consoleTheme(),
			})
			if err == nil {
				return &consoleHost{term: term, key: key}, nil
			}
		}
	}
	if vm.ConsoleTab == ConsoleADB {
		lines := newLineConsole("adb 命令 · 例如 shell ls /sdcard、pull /sdcard/a.mp4 .", nil)
		// The command runs off the input path: a pull takes seconds, and
		// the console must keep echoing while it does.
		lines.onLine = func(line string) { go vm.runADBLine(profile, session, lines, line) }
		term, err := newTerminal(terminal.Options{Conn: lines, Theme: consoleTheme()})
		if err != nil {
			return nil, err
		}
		return &consoleHost{term: term, key: key, lines: lines}, nil
	}
	// No connection, or no adb to drive it: an idle console.
	lines := newLineConsole(IdleConsoleText, nil)
	lines.onLine = vm.idleLineHandler(lines)
	term, err := newTerminal(terminal.Options{Conn: lines, Theme: consoleTheme()})
	if err != nil {
		return nil, err
	}
	return &consoleHost{term: term, key: key, lines: lines}, nil
}

// idleLineHandler refuses every command of an unconnected device: the line is
// not sent anywhere, the console says why, and the user gets a toast.
func (vm *ViewModel) idleLineHandler(lines *lineConsole) func(string) {
	return func(string) {
		lines.push("\r\n" + IdleConsoleText + "\r\n")
		lines.prompt()
		vm.FailText(IdleConsoleText)
	}
}

// runADBLine runs one adb command for the console and prints its output.
func (vm *ViewModel) runADBLine(profile core.Profile, session core.Session, lines *lineConsole, line string) {
	args := strings.Fields(line)
	if len(args) == 0 {
		lines.prompt()
		return
	}
	if session.State != core.StateOnline || session.Serial == "" {
		lines.push("\r\n" + IdleConsoleText + "\r\n")
		vm.FailText(IdleConsoleText)
		return
	}
	output, err := vm.Manager.RunADB(context.Background(), profile.ID, args, 2*time.Minute)
	if err != nil {
		lines.push("\r\n" + err.Error() + "\r\n")
		lines.prompt()
		vm.Fail(err)
		return
	}
	if output != "" {
		lines.push("\r\n" + strings.ReplaceAll(strings.TrimRight(output, "\n"), "\n", "\r\n") + "\r\n")
	}
	lines.prompt()
}

// closeConsole stops a console's program. It is a variable so a test can watch
// that it does not run in the frame that leaves the tab.
var closeConsole = func(host *consoleHost) {
	if host.term != nil {
		_ = host.term.Close()
	}
	if host.lines != nil {
		_ = host.lines.Close()
	}
}

// dropConsole closes and forgets a device's console, off the render thread:
// closing a terminal stops the adb shell behind it, and on Windows that is a
// `taskkill` that took the better part of a second in the frame that left the
// page.
func (vm *ViewModel) dropConsole(id uuid.UUID) {
	host, ok := vm.consoles[id]
	if !ok {
		return
	}
	delete(vm.consoles, id)
	go closeConsole(host)
}

// CloseConsoles stops every embedded console and waits for it. The app calls it
// on the way out: exiting first would leave the adb shells behind.
func (vm *ViewModel) CloseConsoles() {
	for id := range vm.consoles {
		host := vm.consoles[id]
		delete(vm.consoles, id)
		if host != nil {
			closeConsole(host)
		}
	}
}

// lineConsole is an in-memory console: it prints a banner, echoes what is
// typed, and hands each entered line to onLine.
type lineConsole struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []byte
	line   []byte
	banner string
	onLine func(string)
	closed bool
	// esc and csi track a key's escape sequence so it is not echoed as text.
	esc, csi bool
}

// newLineConsole starts a console with its banner and prompt on screen.
func newLineConsole(banner string, onLine func(string)) *lineConsole {
	s := &lineConsole{banner: banner, onLine: onLine}
	s.cond = sync.NewCond(&s.mu)
	if banner != "" {
		s.push(banner + "\r\n")
	}
	s.prompt()
	return s
}

func (s *lineConsole) push(text string) {
	s.mu.Lock()
	s.queue = append(s.queue, text...)
	s.cond.Signal()
	s.mu.Unlock()
}

// prompt prints the input prompt.
func (s *lineConsole) prompt() { s.push("\r\n$ ") }

// Read hands the queued output to the terminal.
func (s *lineConsole) Read(p []byte) (int, error) {
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

// Write echoes the keystrokes and hands a completed line to onLine.
func (s *lineConsole) Write(p []byte) (int, error) {
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
			s.mu.Lock()
			line := string(s.line)
			s.line = nil
			s.mu.Unlock()
			s.push("\r\n")
			if s.onLine != nil {
				s.onLine(line)
			}
		case b == 0x7f || b == 0x08:
			if s.backspace() {
				s.push("\b \b")
			}
		case b == 0x03:
			s.mu.Lock()
			s.line = nil
			s.mu.Unlock()
			s.push("^C")
			s.prompt()
		case b >= 0x20:
			s.mu.Lock()
			s.line = append(s.line, b)
			s.mu.Unlock()
			s.push(string(b))
		}
	}
	return len(p), nil
}

// backspace drops the last byte, reporting whether anything was removed.
func (s *lineConsole) backspace() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.line) == 0 {
		return false
	}
	s.line = s.line[:len(s.line)-1]
	return true
}

// Close ends the console.
func (s *lineConsole) Close() error {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

// Line is what the user typed so far, without the trailing Enter.
func (s *lineConsole) Line() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.line)
}
