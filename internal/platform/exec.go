package platform

import "os/exec"

// Command builds a child process the way this app runs them: in a process
// group of its own, so terminating it stops whatever it spawned, and — on
// Windows — with no console of its own.
//
// Every child goes through here. The packaged app is a GUI executable with no
// console: when a child such as adb or taskkill was allowed to allocate one,
// Windows drew a black window on screen for each command, and with a status
// poll every three seconds per device the screen filled with them. Only the
// built app showed it, because a child started from a terminal inherits that
// terminal's console.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureProcess(cmd)
	return cmd
}

// VisibleCommand builds a child that is supposed to show a window of its own:
// the file manager or shell we ask to reveal a path. It keeps the process
// group, and drops the console hiding that also suppresses the window the user
// asked for — an explorer started hidden is indistinguishable from 打开没反应.
func VisibleCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureVisibleProcess(cmd)
	return cmd
}
