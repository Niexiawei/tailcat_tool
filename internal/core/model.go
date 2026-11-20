package core

// ConnectionState is the lifecycle of one device's Tailcat/ADB channel.
// The string values are wire-compatible with the Swift build's
// configuration and UI state names; the labels are what the user reads.
type ConnectionState string

// The connection states. Order matters only for documentation.
const (
	StateDisconnected  ConnectionState = "disconnected"
	StateConnecting    ConnectionState = "connecting"
	StateOnline        ConnectionState = "online"
	StateOffline       ConnectionState = "offline"
	StateUnauthorized  ConnectionState = "unauthorized"
	StateDisconnecting ConnectionState = "disconnecting"
	StateFailed        ConnectionState = "failed"
	// StateNeedsAction means something outside the app changed the channel
	// (an external adb disconnect, a moved local port, a lost listener) and
	// the user has to decide what happens next. Nothing retries in this state.
	StateNeedsAction ConnectionState = "needsAction"
	// StateTunnelLost means our Tailcat tunnel stopped answering: the channel
	// is gone even though the local ADB entry may still look fine. It is not
	// needsAction — nothing outside changed our setup, and the app is already
	// rebuilding the channel on its own.
	StateTunnelLost ConnectionState = "tunnelLost"
)

// Label returns the Chinese label shown in the UI.
func (s ConnectionState) Label() string {
	switch s {
	case StateDisconnected, ConnectionState(""):
		// The zero value is what a session that nobody touched yet carries; a
		// blank label reads as "no state", which is worse than "not connected".
		return "未连接"
	case StateConnecting:
		return "正在连接"
	case StateOnline:
		return "ADB 已连接"
	case StateOffline:
		return "ADB 离线"
	case StateUnauthorized:
		return "ADB 待授权"
	case StateDisconnecting:
		return "正在断开"
	case StateFailed:
		return "连接失败"
	case StateNeedsAction:
		return "等待处理"
	case StateTunnelLost:
		return "Tailcat 断开"
	default:
		return string(s)
	}
}

// String makes the label the default formatting of a state.
func (s ConnectionState) String() string { return s.Label() }

// Busy reports whether a transition is in flight.
func (s ConnectionState) Busy() bool {
	return s == StateConnecting || s == StateDisconnecting
}

// TaskState is the lifecycle of one background device task.
type TaskState string

// The task states.
const (
	TaskRunning   TaskState = "running"
	TaskSucceeded TaskState = "succeeded"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
)

// Label returns the Chinese label shown in the UI.
func (s TaskState) Label() string {
	switch s {
	case TaskRunning:
		return "执行中"
	case TaskSucceeded:
		return "已完成"
	case TaskFailed:
		return "失败"
	case TaskCancelled:
		return "已取消"
	default:
		return string(s)
	}
}

// String makes the label the default formatting of a task state.
func (s TaskState) String() string { return s.Label() }
