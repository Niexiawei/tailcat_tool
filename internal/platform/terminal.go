package platform

import "errors"

// ErrorTerminalUnavailable means no terminal emulator was found.
var ErrorTerminalUnavailable = errors.New("未找到可用的终端程序，请在设置中手动打开 adb shell")
