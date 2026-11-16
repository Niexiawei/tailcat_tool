package core

import (
	"time"

	"github.com/google/uuid"
)

// Session is everything the UI needs to render one device's channel.
type Session struct {
	State        ConnectionState
	Serial       string // "127.0.0.1:<port>", empty when there is no channel
	Port         int
	Model        string
	Product      string
	Android      string
	DeviceSerial string
	ShellID      string
	Route        string
	Err          string
	Logs         []SessionLog
}

// SessionLog is one timestamped line of channel output.
type SessionLog struct {
	At   time.Time
	Text string
}

// NewSession returns a session with the same placeholder values the Swift UI
// used before any device information arrives.
func NewSession() Session {
	return Session{
		Model:        PlaceholderUnknown,
		Product:      PlaceholderUnknown,
		Android:      PlaceholderUnknown,
		DeviceSerial: PlaceholderUnknown,
		ShellID:      PlaceholderUnknown,
		Route:        PlaceholderNotProbed,
	}
}

const (
	// PlaceholderUnknown stands in for a value the app has not read yet.
	PlaceholderUnknown = "—"
	// PlaceholderNotProbed is the route before any link probe ran.
	PlaceholderNotProbed = "未探测"
)

// Profile is a saved device: identity, preferred ports and relay source.
type Profile struct {
	ID uuid.UUID `json:"id"`
	// Name is the user-visible label.
	Name string `json:"name"`
	// PreferredPort is the fixed local port, or nil for automatic allocation.
	PreferredPort *int `json:"preferredPort,omitempty"`
	// RemotePort is the ADB port on the device side.
	RemotePort int `json:"remotePort"`
	// RelaySource is a DERP map URL or inline JSON; empty means the official map.
	RelaySource string `json:"relaySource,omitempty"`
	// DirectADB connects straight to ADB, without a Tailcat channel.
	DirectADB bool `json:"directADB,omitempty"`
	// DirectTarget is the ADB endpoint: "host:port" for a networked device,
	// or a USB serial such as "emulator-5554". Not a secret.
	DirectTarget string `json:"directTarget,omitempty"`
}

// RelayHistoryEntry is one custom DERP source the user has used before. It is
// what the editor offers as 「之前用过的中继」; a management page comes later.
type RelayHistoryEntry struct {
	Source string `json:"source"`
	// Name is derived from the source when it is first recorded and is only a
	// label: Source is what goes back into a device.
	Name     string    `json:"name"`
	LastUsed time.Time `json:"lastUsed"`
}

// RelayPreset is a saved DERP map source the user can pick in the editor.
type RelayPreset struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Source string    `json:"source"`
}
