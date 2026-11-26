package core

import "fmt"

// Error is a user-facing failure. Its message is shown verbatim in the UI, so
// it must already be redacted and written in the app's voice.
type Error string

// Error implements error.
func (e Error) Error() string { return string(e) }

// Errorf builds an Error with formatting.
func Errorf(format string, args ...any) error {
	return Error(fmt.Sprintf(format, args...))
}

// ErrCancelled reports that an operation stopped because it was cancelled.
// Callers compare with errors.Is.
var ErrCancelled = Error("操作已取消")

// ErrUnsupported reports a capability this platform does not provide.
var ErrUnsupported = Error("当前平台不支持该功能")

// ErrDeviceNotReady means the channel exists but ADB has not reached the
// `device` state yet: connecting, disconnecting, or still offline. It is
// transient, and callers must not cache it as a failure.
var ErrDeviceNotReady = Error("该设备的 ADB 尚未进入 device 状态")
