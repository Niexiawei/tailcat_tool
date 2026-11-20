package core

// User-facing strings live here so a wording change is one edit and the tests
// can assert them verbatim. Keep the wording identical to the Swift build
// unless a task says otherwise.
const (
	// MsgRedacted replaces a Tailcat connection address in any log or output.
	MsgRedacted = "[连接凭据已隐藏]"
	// MsgLineTruncated is emitted when a single output line exceeds the buffer.
	MsgLineTruncated = "[输出行过长，已截断]"
)

// Why reading an application's name and icon produced nothing. The full
// sentences go into the row's tooltip; the labels are what fits in a cell.
const (
	// ReasonNoAppPackage is final: the device has no APK for this process, so no
	// amount of retrying will name it.
	ReasonNoAppPackage  = "设备上没有这个应用的应用包"
	ReasonAPKTooLarge   = "应用包过大，未读取"
	ReasonAPKTimeout    = "读取应用包超时"
	ReasonAPKUnreadable = "应用包无法解析"

	// LabelNoAppPackage and LabelReadFailed are the short row labels, empty
	// Reason falls back to LabelReadFailed.
	LabelNoAppPackage = "无应用包"
	LabelReadFailed   = "读取失败"
)

// MsgTaskOutputMissing is what the output dialog says when a task's record is
// gone (cleared history, or a row from another machine).
const MsgTaskOutputMissing = "任务输出不存在，可能已被清理"

// MsgAdbStuck means the tunnel answers but the device's ADB does not: it is a
// device-side outage, not a channel one, so the recovery is adb's own retry.
const MsgAdbStuck = "设备在 4 秒内没有应答，ADB 可能已卡住"

// MsgTunnelLost is the session's explanation while the channel is being
// rebuilt.
const MsgTunnelLost = "Tailcat 通道已断开，正在重建通道"

// MsgTunnelRebuildFailed is what the device says after the rebuild budget is
// spent and nothing is retried any more.
const MsgTunnelRebuildFailed = "Tailcat 通道重建失败，请检查网络后重新连接"
