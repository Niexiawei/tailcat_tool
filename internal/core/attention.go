package core

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AttentionKind says why a device needs the user's decision.
type AttentionKind string

// The kinds of external interference the app reports.
const (
	// AttentionSerialDetached: the user removed our ADB entry with
	// "adb disconnect".
	AttentionSerialDetached AttentionKind = "serialDetached"
	// AttentionPortChanged: the same device now answers on another local port,
	// probably because another program mapped it.
	AttentionPortChanged AttentionKind = "portChanged"
	// AttentionPortDrift: our own local port is not the one that was agreed.
	AttentionPortDrift AttentionKind = "portDrift"
)

// Label is the sentence the dialog shows.
func (k AttentionKind) Label() string {
	switch k {
	case AttentionSerialDetached:
		return "本地 ADB 入口已被外部移除"
	case AttentionPortChanged:
		return "该设备已在另一个本地端口出现"
	case AttentionPortDrift:
		return "本机入口端口已变化"
	default:
		return string(k)
	}
}

// Attention is one device that needs the user to choose.
type Attention struct {
	DeviceID     uuid.UUID
	Kind         AttentionKind
	Serial       string
	DetectedPort int
	Detail       string
	DetectedAt   time.Time
}

// Label is the sentence the dialog shows for this attention.
func (a Attention) Label() string { return a.Kind.Label() }

// ADBDevice is one row of "adb devices -l".
type ADBDevice struct {
	Serial  string
	State   ConnectionState
	Model   string
	Product string
	Device  string
}

// ParseADBDevices reads the output of "adb devices -l".
func ParseADBDevices(text string) []ADBDevice {
	var out []ADBDevice
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "127.0.0.1:") {
			continue
		}
		device := ADBDevice{Serial: fields[0]}
		switch fields[1] {
		case "device":
			device.State = StateOnline
		case "unauthorized":
			device.State = StateUnauthorized
		default:
			device.State = StateOffline
		}
		for _, field := range fields[2:] {
			switch {
			case strings.HasPrefix(field, "model:"):
				device.Model = strings.TrimPrefix(field, "model:")
			case strings.HasPrefix(field, "product:"):
				device.Product = strings.TrimPrefix(field, "product:")
			case strings.HasPrefix(field, "device:"):
				device.Device = strings.TrimPrefix(field, "device:")
			}
		}
		out = append(out, device)
	}
	return out
}

// DetectInterference decides whether the user (or another program) changed the
// channel behind our back. It is a pure function so every rule is testable.
//
// The caller passes the device's current session, whether the same model and
// device code are visible on another local port, and the parsed device list.
func DetectInterference(session SeriallessSession, devices []ADBDevice) *Attention {
	serial := session.Serial
	if serial == "" {
		return nil
	}
	for _, device := range devices {
		if device.Serial == serial {
			// Our entry is still there: nothing external happened, whatever
			// state it is in.
			return nil
		}
	}
	// Our entry is gone. If the same device answers elsewhere, say so; the
	// identity match keeps two identical cars apart.
	if session.DeviceSerial != "" || session.Model != "" {
		for _, device := range devices {
			if device.Serial == serial {
				continue
			}
			if device.Device != "" && device.Device == session.DeviceCode ||
				device.Model != "" && device.Model == session.Model && device.Device == session.DeviceCode {
				return &Attention{
					Kind:         AttentionPortChanged,
					Serial:       serial,
					DetectedPort: portOf(device.Serial),
					Detail:       "该设备已在 " + device.Serial + " 出现（可能由其他程序映射）",
					DetectedAt:   time.Now(),
				}
			}
		}
	}
	return &Attention{
		Kind:       AttentionSerialDetached,
		Serial:     serial,
		Detail:     "本地 ADB 入口 " + serial + " 已被外部移除",
		DetectedAt: time.Now(),
	}
}

func portOf(serial string) int {
	_, port := splitSerial(serial)
	return port
}

func splitSerial(serial string) (string, int) {
	index := strings.LastIndex(serial, ":")
	if index < 0 {
		return serial, 0
	}
	port := 0
	for _, r := range serial[index+1:] {
		if r < '0' || r > '9' {
			return serial[:index], 0
		}
		port = port*10 + int(r-'0')
	}
	return serial[:index], port
}

// SeriallessSession is the subset of Session the detector reads. It keeps the
// rule independent of the rest of the UI state.
type SeriallessSession struct {
	Serial       string
	Model        string
	DeviceCode   string
	DeviceSerial string
}

// Attentions returns the devices that are waiting for a decision.
func (m *Manager) Attentions() []Attention {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Attention, 0, len(m.attention))
	for _, attention := range m.attention {
		out = append(out, attention)
	}
	return out
}

// AckAttention dismisses the dialog but keeps the device in "needs action".
func (m *Manager) AckAttention(id uuid.UUID) {
	m.mu.Lock()
	delete(m.attention, id)
	m.notify()
	m.mu.Unlock()
}

// ForceAttentionForTest records an attention without sampling adb. It exists
// so the view tests can render the dialog without a device.
func (m *Manager) ForceAttentionForTest(id uuid.UUID, kind AttentionKind, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attention[id] = Attention{
		DeviceID:   id,
		Kind:       kind,
		Detail:     detail,
		DetectedAt: time.Now(),
	}
	m.setStateLocked(id, func(s *Session) {
		s.State = StateNeedsAction
		s.Err = kind.Label()
	})
	m.notify()
}

// detectInterference samples "adb devices -l" and records what it finds.
func (m *Manager) detectInterference(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	session := m.sessions[id]
	m.mu.Unlock()
	if runtime == nil || session.Serial == "" {
		return
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    runtime.adb,
		Args:    []string{"devices", "-l"},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil || res.ExitCode != 0 {
		return
	}
	devices := ParseADBDevices(res.Output)
	attention := DetectInterference(SeriallessSession{
		Serial:       session.Serial,
		Model:        session.Model,
		DeviceCode:   session.Product,
		DeviceSerial: session.DeviceSerial,
	}, devices)
	if attention == nil {
		return
	}
	attention.DeviceID = id

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isCurrentLocked(id, generation) {
		return
	}
	if existing, ok := m.attention[id]; ok && existing.Kind == attention.Kind {
		return
	}
	m.attention[id] = *attention
	m.setStateLocked(id, func(s *Session) {
		s.State = StateNeedsAction
		s.Err = attention.Label()
	})
	m.logLocked(id, attention.Detail)
	m.notify()
	m.stopMonitorLocked(id)
}

// SwitchPort remembers a new local port and reconnects on it.
func (m *Manager) SwitchPort(ctx context.Context, id uuid.UUID, port int, remember bool) error {
	m.mu.Lock()
	var index = -1
	for i := range m.profiles {
		if m.profiles[i].ID == id {
			index = i
		}
	}
	if index < 0 {
		m.mu.Unlock()
		return Error("设备不存在")
	}
	// port 0 asks for automatic allocation, which clears the fixed port.
	var saved *int
	if remember && port > 0 {
		value := port
		saved = &value
	}
	previous := m.profiles[index].PreferredPort
	m.profiles[index].PreferredPort = saved
	if err := m.persistLocked(); err != nil {
		m.profiles[index].PreferredPort = previous
		m.mu.Unlock()
		return err
	}
	delete(m.attention, id)
	m.mu.Unlock()

	m.Disconnect(ctx, id)
	return m.Connect(ctx, id)
}
