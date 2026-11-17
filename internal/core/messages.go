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
