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
