package core

import (
	"context"
	"sort"
	"strconv"
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
	// AttentionServerRestart: several devices lost their ADB entry at once,
	// which is what "adb kill-server" does. One prompt for the whole fleet.
	AttentionServerRestart AttentionKind = "serverRestart"
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
	case AttentionServerRestart:
		return "ADB 服务被重启，多台设备受影响"
	default:
		return string(k)
	}
}

// Attention is one device that needs the user to choose. When several devices
// lost their entry together, one attention covers all of them: DeviceID is the
// first, and DeviceIDs lists them in the order they were reported.
type Attention struct {
	DeviceID     uuid.UUID
	DeviceIDs    []uuid.UUID
	Kind         AttentionKind
	Serial       string
	DetectedPort int
	Detail       string
	DetectedAt   time.Time
}

// Devices returns every device the attention covers, the primary one first.
// A single-device attention made before DeviceIDs existed still answers.
func (a Attention) Devices() []uuid.UUID {
	if len(a.DeviceIDs) > 0 {
		return a.DeviceIDs
	}
	if a.DeviceID == uuid.Nil {
		return nil
	}
	return []uuid.UUID{a.DeviceID}
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

// Detachment is one device losing its ADB entry.
type Detachment struct {
	DeviceID uuid.UUID
	Serial   string
	At       time.Time
}

// DetachmentGroup is detachments that happened close enough together to be one
// event: "adb kill-server" drops every device at once, a user's own disconnect
// drops one.
type DetachmentGroup struct {
	DeviceIDs []uuid.UUID
	Serials   []string
	At        time.Time
}

// AggregateDetachments merges the detachments within window of each group's
// first event. It is a pure function so the rule is testable without adb.
func AggregateDetachments(events []Detachment, window time.Duration) []DetachmentGroup {
	sorted := make([]Detachment, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	var groups []DetachmentGroup
	var current *DetachmentGroup
	for _, event := range sorted {
		if current != nil && !event.At.After(current.At.Add(window)) {
			current.DeviceIDs = append(current.DeviceIDs, event.DeviceID)
			current.Serials = append(current.Serials, event.Serial)
			continue
		}
		groups = append(groups, DetachmentGroup{
			DeviceIDs: []uuid.UUID{event.DeviceID},
			Serials:   []string{event.Serial},
			At:        event.At,
		})
		current = &groups[len(groups)-1]
	}
	return groups
}

// SeriallessSession is the subset of Session the detector reads. It keeps the
// rule independent of the rest of the UI state.
type SeriallessSession struct {
	Serial       string
	Model        string
	DeviceCode   string
	DeviceSerial string
}

// detachWindow is how close together detachments must be to count as one
// event. Three seconds is longer than a fleet's tickers take to notice the same
// "adb kill-server", and short enough that two user disconnects a minute apart
// stay two prompts.
const detachWindow = 3 * time.Second

// attentionForLocked finds the attention covering id, whether id is its
// primary device or one it was aggregated with. Scanning a map of at most a
// handful of entries beats a second index that has to be kept in step with
// every delete.
func (m *Manager) attentionForLocked(id uuid.UUID) (Attention, bool) {
	if attention, ok := m.attention[id]; ok {
		return attention, true
	}
	for _, attention := range m.attention {
		for _, member := range attention.DeviceIDs {
			if member == id {
				return attention, true
			}
		}
	}
	return Attention{}, false
}

// recordDetachmentLocked turns one device's serial loss into an attention,
// merging it with the fleet whenever the detachments are close together. The
// caller has already checked isCurrentLocked and holds m.mu.
func (m *Manager) recordDetachmentLocked(id uuid.UUID, attention Attention) Attention {
	detached := Detachment{DeviceID: id, Serial: attention.Serial, At: attention.DetectedAt}
	kept := make([]Detachment, 0, len(m.detachments)+1)
	for _, event := range m.detachments {
		if attention.DetectedAt.Sub(event.At) <= detachWindow {
			kept = append(kept, event)
		}
	}
	kept = append(kept, detached)
	m.detachments = kept

	groups := AggregateDetachments(kept, detachWindow)
	group := groups[len(groups)-1]
	for _, candidate := range groups {
		for _, member := range candidate.DeviceIDs {
			if member == id {
				group = candidate
			}
		}
	}
	if len(group.DeviceIDs) < 2 {
		attention.DeviceIDs = []uuid.UUID{id}
		m.attention[id] = attention
		return attention
	}

	// Several devices lost their entry at once: one prompt, and the device that
	// noticed first carries it.
	primary := group.DeviceIDs[0]
	merged, ok := m.attention[primary]
	if !ok {
		// The group's first device was acknowledged while the others were still
		// being reported; the prompt comes back for the group as a whole.
		merged = attention
		merged.DeviceID = primary
		merged.DetectedAt = group.At
	}
	merged.Kind = AttentionServerRestart
	merged.DeviceIDs = group.DeviceIDs
	merged.Detail = strconv.Itoa(len(group.DeviceIDs)) + " 台设备的本地 ADB 入口同时消失"
	m.attention[primary] = merged
	return merged
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
// Acknowledging any device of an aggregated prompt dismisses the whole group:
// it was one event, and asking again per device would be asking again for an
// answer the user already gave.
func (m *Manager) AckAttention(id uuid.UUID) {
	m.mu.Lock()
	answered := map[uuid.UUID]bool{}
	if attention, ok := m.attentionForLocked(id); ok {
		delete(m.attention, attention.DeviceID)
		for _, member := range attention.Devices() {
			answered[member] = true
		}
	} else {
		delete(m.attention, id)
		answered[id] = true
	}
	// The answered detachments leave the window too: a device that notices the
	// same outage a second later must not rebuild the prompt around them.
	kept := m.detachments[:0]
	for _, detachment := range m.detachments {
		if !answered[detachment.DeviceID] {
			kept = append(kept, detachment)
		}
	}
	m.detachments = kept
	m.notify()
	m.mu.Unlock()
}

// ForceSessionStateForTest sets one device's session state without sampling
// anything. It exists so the view tests and the screenshot harness can render a
// state that only a real outage reaches.
func (m *Manager) ForceSessionStateForTest(id uuid.UUID, state ConnectionState, errText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(id, func(s *Session) {
		s.State = state
		s.Err = errText
	})
	m.notify()
}

// ForceSessionUsersForTest sets one device's user list without asking a device.
func (m *Manager) ForceSessionUsersForTest(id uuid.UUID, users []DeviceUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(id, func(s *Session) { s.Users = users })
	m.notify()
}

// ForceAttentionForTest records an attention without sampling adb. It exists
// so the view tests can render the dialog without a device.
func (m *Manager) ForceAttentionForTest(id uuid.UUID, kind AttentionKind, detail string) {
	m.ForceAttentionGroupForTest([]uuid.UUID{id}, kind, detail)
}

// ForceAttentionGroupForTest records one attention covering several devices.
func (m *Manager) ForceAttentionGroupForTest(ids []uuid.UUID, kind AttentionKind, detail string) {
	if len(ids) == 0 {
		return
	}
	id := ids[0]
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attention[id] = Attention{
		DeviceID:   id,
		DeviceIDs:  append([]uuid.UUID(nil), ids...),
		Kind:       kind,
		Detail:     detail,
		DetectedAt: time.Now(),
	}
	for _, member := range ids {
		m.setStateLocked(member, func(s *Session) {
			s.State = StateNeedsAction
			s.Err = kind.Label()
		})
	}
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
	if existing, ok := m.attentionForLocked(id); ok && existing.Kind == attention.Kind {
		return
	}
	// The device waits for the user from this moment on: retryOffline may not
	// connect it back on its own.
	saved := m.recordDetachmentLocked(id, *attention)
	for _, member := range saved.Devices() {
		member := member
		m.setStateLocked(member, func(s *Session) {
			s.State = StateNeedsAction
			s.Err = saved.Label()
		})
	}
	m.logLocked(id, saved.Detail)
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
	if attention, ok := m.attentionForLocked(id); ok {
		delete(m.attention, attention.DeviceID)
	} else {
		delete(m.attention, id)
	}
	m.mu.Unlock()

	m.Disconnect(ctx, id)
	return m.Connect(ctx, id)
}

// ReconnectAll connects every device an aggregated prompt named, skipping the
// ones that are already fine. One device failing to come back is logged, not
// fatal: the others have their own channels and their own outcomes.
func (m *Manager) ReconnectAll(ctx context.Context, ids []uuid.UUID) error {
	for _, id := range ids {
		if m.Session(id).State == StateOnline {
			continue
		}
		if err := m.Connect(ctx, id); err != nil {
			m.log(id, "重新连接失败："+err.Error())
		}
	}
	return nil
}
