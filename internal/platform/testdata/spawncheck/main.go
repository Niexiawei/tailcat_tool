//go:build windows

// Command spawncheck answers one question that a `go test` binary cannot:
// when the app spawns a child, does Windows put a console window on screen?
//
// It has to be built as a GUI executable, because that is the only parent
// without a console of its own — a console parent hands its console to every
// child and no window ever appears, which is why this bug only showed in the
// packaged app.
//
//	go build -ldflags "-H windowsgui" -o spawncheck.exe ./internal/platform/testdata/spawncheck
//	spawncheck.exe after    # platform.Command, which is how the app spawns children
//	spawncheck.exe before   # CREATE_NEW_PROCESS_GROUP only, as before the fix
//
// Exit status 0 means the child owned no visible window.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/platform"
	"golang.org/x/sys/windows"
)

// oldFlags is what ConfigureProcess set before the fix: the process group
// alone, which keeps the child's console window.
const oldFlags = 0x00000200

func main() {
	mode := "after"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	// A child that lives for a few seconds, so the window (if any) is there to
	// be seen.
	var cmd *exec.Cmd
	if mode == "before" {
		cmd = exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: oldFlags}
	} else {
		cmd = platform.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("start:", err)
		os.Exit(2)
	}
	pid := uint32(cmd.Process.Pid)
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	classes := map[string]bool{}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, class := range windowClassesOf(pid) {
			classes[class] = true
		}
		time.Sleep(30 * time.Millisecond)
	}

	if len(classes) == 0 {
		fmt.Printf("%s: the child owned no visible window\n", mode)
		return
	}
	for class := range classes {
		fmt.Printf("%s: the child owned a visible window of class %q\n", mode, class)
	}
	os.Exit(1)
}

// windowClassesOf lists the classes of the visible windows a process owns.
func windowClassesOf(pid uint32) []string {
	var classes []string
	callback := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var owner uint32
		if _, err := windows.GetWindowThreadProcessId(hwnd, &owner); err != nil || owner != pid {
			return 1
		}
		if !windows.IsWindowVisible(hwnd) {
			return 1
		}
		var class [256]uint16
		copied, err := windows.GetClassName(hwnd, &class[0], int32(len(class)))
		if err == nil && copied > 0 {
			classes = append(classes, windows.UTF16ToString(class[:copied]))
		}
		return 1
	})
	_ = windows.EnumWindows(callback, nil)
	return classes
}
