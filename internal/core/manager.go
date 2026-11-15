package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Defaults for the monitoring loop. Tests override them with tiny values.
const (
	defaultMonitorInterval = 3 * time.Second
	connectPollInterval    = 400 * time.Millisecond
	connectPollAttempts    = 10
	maxOfflineRetries      = 6
	adbConnectTimeout      = 15 * time.Second
	adbStateTimeout        = 4 * time.Second
)

// ChannelOpener opens device channels (the forwarder in production).
type ChannelOpener interface {
	Open(ctx context.Context, req ForwardRequest, onLog func(string)) (*ForwardConn, error)
}

// Manager owns every device channel. All methods are safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	changed chan struct{}

	config      *ConfigStore
	credentials CredentialStore
	identities  *IdentityStore
	runner      Runner
	forwarder   ChannelOpener
	locate      LocateTool

	profiles        []Profile
	temporary       []Profile
	tempCredentials map[uuid.UUID]string
	sessions        map[uuid.UUID]Session
	tasks           []Task
	relayPresets    []RelayPreset
	tools           ToolSettings

	generations map[uuid.UUID]uuid.UUID
	runtimes    map[uuid.UUID]*runtimeState
	monitors    map[uuid.UUID]context.CancelFunc
	retryCounts map[uuid.UUID]int
	nextRetries map[uuid.UUID]time.Time
	taskCancels map[uuid.UUID]context.CancelFunc
	taskDone    map[uuid.UUID]chan struct{}
	attention   map[uuid.UUID]Attention
	pinger      *Pinger
	recorder    Recorder
	checker     *Checker

	notice              string
	nextTemporaryNumber int

	monitorInterval time.Duration
	connectPoll     time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
}

type runtimeState struct {
	// conn is nil for a direct ADB connection, which has no Tailcat channel.
	conn       *ForwardConn
	serial     string
	adb        string
	generation uuid.UUID
}

// ManagerOptions wires a Manager. Only Runner and Credentials are required.
type ManagerOptions struct {
	Config        *ConfigStore
	Configuration Configuration
	Credentials   CredentialStore
	Identities    *IdentityStore
	Runner        Runner
	Forwarder     ChannelOpener
	// Prober replaces the link probe; tests inject a fake so no network is
	// touched.
	Prober Prober
	// Recorder persists tasks, logs and reachability results; nil keeps
	// everything in memory.
	Recorder        Recorder
	Locate          LocateTool
	MonitorInterval time.Duration
	// ConnectPollInterval is the delay between get-state polls while
	// connecting; tests use a small value.
	ConnectPollInterval time.Duration
}

// NewManager builds a manager from options.
func NewManager(opts ManagerOptions) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	interval := opts.MonitorInterval
	if interval <= 0 {
		interval = defaultMonitorInterval
	}
	pollInterval := opts.ConnectPollInterval
	if pollInterval <= 0 {
		pollInterval = connectPollInterval
	}
	m := &Manager{
		changed:             make(chan struct{}, 1),
		config:              opts.Config,
		credentials:         opts.Credentials,
		identities:          opts.Identities,
		runner:              opts.Runner,
		forwarder:           opts.Forwarder,
		locate:              opts.Locate,
		profiles:            opts.Configuration.Profiles,
		sessions:            map[uuid.UUID]Session{},
		tools:               opts.Configuration.Tools,
		relayPresets:        opts.Configuration.RelayPresets,
		tempCredentials:     map[uuid.UUID]string{},
		generations:         map[uuid.UUID]uuid.UUID{},
		runtimes:            map[uuid.UUID]*runtimeState{},
		monitors:            map[uuid.UUID]context.CancelFunc{},
		retryCounts:         map[uuid.UUID]int{},
		nextRetries:         map[uuid.UUID]time.Time{},
		taskCancels:         map[uuid.UUID]context.CancelFunc{},
		taskDone:            map[uuid.UUID]chan struct{}{},
		attention:           map[uuid.UUID]Attention{},
		nextTemporaryNumber: 1,
		monitorInterval:     interval,
		connectPoll:         pollInterval,
		ctx:                 ctx,
		cancel:              cancel,
	}
	if m.credentials == nil {
		m.credentials = NewMemoryCredentialStore()
	}
	if m.runner == nil {
		m.runner = NewExecRunner()
	}
	if m.identities == nil {
		m.identities = NewIdentityStore("")
	}
	if m.forwarder == nil {
		m.forwarder = &Forwarder{}
	}
	m.recorder = opts.Recorder
	m.recorder = opts.Recorder
	m.pinger = &Pinger{Identities: m.identities}
	prober := opts.Prober
	if prober == nil {
		prober = m.pinger
	}
	m.checker = NewChecker(prober).WithRecorder(opts.Recorder)
	for _, p := range m.profiles {
		m.sessions[p.ID] = NewSession()
	}
	return m
}

// Notice is the last one-line message for the UI banner (may be empty).
func (m *Manager) Notice() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notice
}

// SetNotice replaces the banner text; an empty string clears it.
func (m *Manager) SetNotice(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notice = text
	m.notify()
}

// Changed reports that state moved; the UI redraws on it.
func (m *Manager) Changed() <-chan struct{} { return m.changed }

func (m *Manager) notify() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

// Profiles returns the saved devices.
func (m *Manager) Profiles() []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Profile(nil), m.profiles...)
}

// TemporaryProfiles returns the throwaway devices of this run.
func (m *Manager) TemporaryProfiles() []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Profile(nil), m.temporary...)
}

// AllProfiles returns saved then temporary devices, in the sidebar's order.
func (m *Manager) AllProfiles() []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Profile, 0, len(m.profiles)+len(m.temporary))
	out = append(out, m.profiles...)
	return append(out, m.temporary...)
}

// Session returns a copy of one device's UI state.
func (m *Manager) Session(id uuid.UUID) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionLocked(id)
}

func (m *Manager) sessionLocked(id uuid.UUID) Session {
	s, ok := m.sessions[id]
	if !ok {
		return NewSession()
	}
	s.Logs = append([]SessionLog(nil), s.Logs...)
	return s
}

// Tools returns the configured tool paths.
func (m *Manager) Tools() ToolSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools
}

// RelayPresets returns the saved DERP sources.
func (m *Manager) RelayPresets() []RelayPreset {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RelayPreset(nil), m.relayPresets...)
}

// Tasks returns a copy of the task history.
func (m *Manager) Tasks() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Task(nil), m.tasks...)
}

// AddressForEditing returns the saved address for the editor.
func (m *Manager) AddressForEditing(id uuid.UUID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	address, err := m.addressLocked(id)
	return address, err
}

func (m *Manager) isTemporaryLocked(id uuid.UUID) bool {
	for _, p := range m.temporary {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (m *Manager) addressLocked(id uuid.UUID) (string, error) {
	if m.isTemporaryLocked(id) {
		return m.tempCredentials[id], nil
	}
	address, ok, err := m.credentials.Read(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return address, nil
}

func (m *Manager) profileLocked(id uuid.UUID) (Profile, bool) {
	for _, p := range m.profiles {
		if p.ID == id {
			return p, true
		}
	}
	for _, p := range m.temporary {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func (m *Manager) setStateLocked(id uuid.UUID, mutate func(*Session)) {
	s := m.sessions[id]
	if s.State == "" {
		s = NewSession()
	}
	mutate(&s)
	m.sessions[id] = s
}

func (m *Manager) logLocked(id uuid.UUID, text string) {
	text = strings.TrimSpace(Redact(text, ""))
	if text == "" {
		return
	}
	if len(text) > 8192 {
		text = text[len(text)-8192:]
	}
	at := time.Now()
	m.setStateLocked(id, func(s *Session) {
		s.Logs = append(s.Logs, SessionLog{At: at, Text: text})
		if len(s.Logs) > 200 {
			s.Logs = append(s.Logs[:0], s.Logs[len(s.Logs)-200:]...)
		}
	})
	if m.recorder != nil {
		_ = m.recorder.RecordLog(LogRecord{DeviceID: id, At: at, Text: text})
	}
}

func (m *Manager) log(id uuid.UUID, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logLocked(id, text)
	m.notify()
}

func (m *Manager) isCurrentLocked(id uuid.UUID, generation uuid.UUID) bool {
	current, ok := m.generations[id]
	return ok && current == generation
}

// Connect opens the device channel and waits for ADB to accept it. It is
// synchronous on purpose: the caller runs it in a goroutine and the UI shows
// "connecting" meanwhile.
func (m *Manager) Connect(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	profile, ok := m.profileLocked(id)
	if !ok {
		m.mu.Unlock()
		return Error("设备不存在")
	}
	if m.sessions[id].State.Busy() {
		m.mu.Unlock()
		return nil
	}
	m.stopMonitorLocked(id)
	previous := m.runtimes[id]
	delete(m.runtimes, id)
	generation := uuid.New()
	m.generations[id] = generation
	m.retryCounts[id] = 0
	delete(m.nextRetries, id)
	m.setStateLocked(id, func(s *Session) {
		s.State = StateConnecting
		s.Err = ""
	})
	// A direct device has no Tailcat address at all, so asking for one would
	// fail with a credential error before the direct branch is reached.
	var address string
	var addrErr error
	if !profile.DirectADB {
		address, addrErr = m.addressLocked(id)
	}
	adbPath := m.tools.FindToolPath(ToolADB, m.locate)
	m.notify()
	m.mu.Unlock()

	oldTasks := m.cancelDeviceTasks(id)

	if previous != nil && previous.conn != nil {
		_ = previous.conn.Close(ctx)
	}
	waitDones(oldTasks)
	if !profile.DirectADB {
		if addrErr != nil {
			return m.failConnect(id, generation, addrErr)
		}
		if !Plausible(address) {
			return m.failConnect(id, generation, Error("设备没有有效连接凭据，请编辑设备后重试"))
		}
	}
	if adbPath == "" {
		return m.failConnect(id, generation, Error("未找到 ADB，请在设置中指定可执行文件的绝对路径"))
	}
	// A direct device never touches Tailcat: no address, identity or relay.
	if profile.DirectADB {
		return m.connectDirect(ctx, id, generation, profile, adbPath)
	}

	relay, err := NormalizeRelay(profile.RelaySource)
	if err != nil {
		return m.failConnect(id, generation, err)
	}
	nodeKey, err := m.identities.NodeKey(ctx, id.String()+"-connection")
	if err != nil {
		return m.failConnect(id, generation, err)
	}

	m.log(id, "正在建立独立 Tailcat 转发通道")
	conn, err := m.forwarder.Open(ctx, ForwardRequest{
		Address:       address,
		PreferredPort: preferredPort(profile),
		RemotePort:    remotePort(profile),
		NodeKey:       nodeKey,
		Relay:         relay,
	}, func(line string) {
		m.mu.Lock()
		current := m.isCurrentLocked(id, generation)
		m.mu.Unlock()
		if current {
			m.log(id, line)
		}
	})
	if err != nil {
		return m.failConnect(id, generation, err)
	}

	m.mu.Lock()
	if !m.isCurrentLocked(id, generation) {
		m.mu.Unlock()
		_ = conn.Close(ctx)
		return nil
	}
	m.runtimes[id] = &runtimeState{conn: conn, serial: conn.Serial, adb: adbPath, generation: generation}
	m.setStateLocked(id, func(s *Session) {
		s.Serial = conn.Serial
		s.Port = conn.Port
	})
	m.notify()
	m.mu.Unlock()

	res, err := m.runner.Run(ctx, Command{
		Path:    adbPath,
		Args:    []string{"connect", conn.Serial},
		Timeout: adbConnectTimeout,
	}, nil)
	if err != nil {
		return m.failConnect(id, generation, err)
	}
	if res.TimedOut {
		m.log(id, "ADB 连接等待超时，通道保留，可重试或等待设备上线")
	} else if trimmed := strings.TrimSpace(res.Output); trimmed != "" {
		m.log(id, trimmed)
	}

	for attempt := 0; attempt < connectPollAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil
		}
		m.refresh(ctx, id, generation)
		state := m.Session(id).State
		if state == StateOnline || state == StateUnauthorized {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.connectPoll):
		}
	}
	m.startMonitor(id, generation)
	return nil
}

func preferredPort(p Profile) int {
	if p.PreferredPort == nil {
		return 0
	}
	return *p.PreferredPort
}

func remotePort(p Profile) int {
	if p.RemotePort <= 0 {
		return 5555
	}
	return p.RemotePort
}

func (m *Manager) failConnect(id uuid.UUID, generation uuid.UUID, err error) error {
	err = sanitizeError(err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isCurrentLocked(id, generation) {
		return nil
	}
	if runtime, ok := m.runtimes[id]; ok {
		delete(m.runtimes, id)
		if runtime.conn != nil {
			_ = runtime.conn.Close(m.ctx)
		}
	}
	m.setStateLocked(id, func(s *Session) {
		s.State = StateFailed
		s.Serial = ""
		s.Port = 0
		s.Err = err.Error()
	})
	m.logLocked(id, "连接失败："+err.Error())
	m.notify()
	return err
}

func sanitizeError(err error) error {
	msg := Redact(err.Error(), "")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "未知错误"
	}
	return Error(msg)
}

// Disconnect closes the channel and removes the ADB entry.
func (m *Manager) Disconnect(ctx context.Context, id uuid.UUID) {
	m.mu.Lock()
	m.stopMonitorLocked(id)
	generation := uuid.New()
	m.generations[id] = generation
	runtime := m.runtimes[id]
	delete(m.runtimes, id)
	delete(m.retryCounts, id)
	delete(m.nextRetries, id)
	m.setStateLocked(id, func(s *Session) {
		s.State = StateDisconnecting
	})
	m.notify()
	m.mu.Unlock()

	jobs := m.cancelDeviceTasks(id)
	waitDones(jobs)

	if runtime != nil {
		if runtime.conn != nil {
			_ = runtime.conn.Close(ctx)
		}
		// A direct USB serial was never "connected", so it needs no
		// disconnect; a networked target does.
		if runtime.conn != nil || strings.Contains(runtime.serial, ":") {
			_, _ = m.runner.Run(ctx, Command{
				Path:    runtime.adb,
				Args:    []string{"disconnect", runtime.serial},
				Timeout: 3 * time.Second,
			}, nil)
		}
	}

	m.mu.Lock()
	if m.isCurrentLocked(id, generation) {
		m.setStateLocked(id, func(s *Session) {
			s.State = StateDisconnected
			s.Serial = ""
			s.Port = 0
			s.Err = ""
			s.Route = PlaceholderNotProbed
		})
		m.logLocked(id, "已断开该设备，其他会话保持")
	}
	m.notify()
	m.mu.Unlock()
}

// DisconnectAll closes every channel.
func (m *Manager) DisconnectAll(ctx context.Context) {
	for _, p := range m.AllProfiles() {
		m.Disconnect(ctx, p.ID)
	}
}

// Shutdown stops everything: monitors, tasks, channels, temporary devices.
func (m *Manager) Shutdown(ctx context.Context) {
	m.cancel()
	m.mu.Lock()
	ids := make([]uuid.UUID, 0, len(m.runtimes))
	for id := range m.runtimes {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(ctx, id)
	}
	m.mu.Lock()
	for _, p := range m.temporary {
		delete(m.sessions, p.ID)
		delete(m.generations, p.ID)
		delete(m.retryCounts, p.ID)
		delete(m.nextRetries, p.ID)
	}
	m.temporary = nil
	m.tempCredentials = map[uuid.UUID]string{}
	m.notify()
	m.mu.Unlock()
	if m.recorder != nil {
		_ = m.recorder.Close()
	}
}

func (m *Manager) stopMonitorLocked(id uuid.UUID) {
	if cancel, ok := m.monitors[id]; ok {
		cancel()
		delete(m.monitors, id)
	}
}

func (m *Manager) startMonitor(id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	m.stopMonitorLocked(id)
	ctx, cancel := context.WithCancel(m.ctx)
	m.monitors[id] = cancel
	interval := m.monitorInterval
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			m.mu.Lock()
			current := m.isCurrentLocked(id, generation)
			m.mu.Unlock()
			if !current {
				return
			}
			m.refresh(ctx, id, generation)
			m.maybeDetectInterference(ctx, id, generation)
			m.retryOffline(ctx, id, generation)
		}
	}()
}

// refresh polls adb get-state and updates the session.
func (m *Manager) refresh(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	if runtime == nil {
		return
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    runtime.adb,
		Args:    []string{"-s", runtime.serial, "get-state"},
		Timeout: adbStateTimeout,
	}, nil)
	output := ""
	if err == nil {
		output = strings.TrimSpace(res.Output)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isCurrentLocked(id, generation) || m.runtimes[id] == nil {
		return
	}
	state := StateOffline
	switch {
	case err == nil && res.ExitCode == 0 && output == "device":
		state = StateOnline
	case strings.Contains(output, "unauthorized"):
		state = StateUnauthorized
	}
	previous := m.sessions[id].State
	m.setStateLocked(id, func(s *Session) {
		s.State = state
		if state == StateOffline {
			s.Err = "本地通道运行中，ADB 尚未上线"
		} else {
			s.Err = ""
		}
	})
	if previous != state {
		m.logLocked(id, state.Label())
	}
	if state == StateOnline {
		m.retryCounts[id] = 0
		delete(m.nextRetries, id)
	}
	m.notify()
}

// maybeDetectInterference runs the external-interference rules when a device is
// offline; it is separate from refresh so it can take the lock again.
func (m *Manager) maybeDetectInterference(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	state := m.sessions[id].State
	_, pending := m.attention[id]
	m.mu.Unlock()
	if pending || state != StateOffline {
		return
	}
	m.detectInterference(ctx, id, generation)
}

// retryOffline re-issues adb connect with a bounded, growing delay.
func (m *Manager) retryOffline(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	state := m.sessions[id].State
	count := m.retryCounts[id]
	next, hasNext := m.nextRetries[id]
	runtime := m.runtimes[id]
	attempt := false
	if state == StateOffline && runtime != nil && count < maxOfflineRetries && (!hasNext || !time.Now().Before(next)) {
		m.retryCounts[id] = count + 1
		m.nextRetries[id] = time.Now().Add(retryDelay(count + 1))
		attempt = true
	}
	n := count + 1
	m.mu.Unlock()

	if !attempt {
		return
	}
	m.log(id, "尝试恢复 ADB 连接（"+strconv.Itoa(n)+"/"+strconv.Itoa(maxOfflineRetries)+"）")
	_, _ = m.runner.Run(ctx, Command{
		Path:    runtime.adb,
		Args:    []string{"connect", runtime.serial},
		Timeout: 10 * time.Second,
	}, nil)
	m.refresh(ctx, id, generation)

	m.mu.Lock()
	if m.isCurrentLocked(id, generation) && n >= maxOfflineRetries && m.sessions[id].State == StateOffline {
		m.setStateLocked(id, func(s *Session) { s.Err = "自动恢复已尝试 6 次，请检查设备后重新连接" })
	}
	m.notify()
	m.mu.Unlock()
}

// retryDelay grows 2s, 4s, 8s, 16s then stays at 30s.
func retryDelay(attempt int) time.Duration {
	seconds := 1 << attempt
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

// RecheckConnections re-arms every monitor, e.g. after the machine wakes.
func (m *Manager) RecheckConnections() {
	m.mu.Lock()
	type pair struct {
		id         uuid.UUID
		generation uuid.UUID
	}
	var pairs []pair
	for id, runtime := range m.runtimes {
		m.retryCounts[id] = 0
		delete(m.nextRetries, id)
		pairs = append(pairs, pair{id: id, generation: runtime.generation})
	}
	m.mu.Unlock()
	for _, p := range pairs {
		m.startMonitor(p.id, p.generation)
	}
}

// cancelDeviceTasks cancels the running tasks of one device and returns the
// channels that close when each task has stopped.
func (m *Manager) cancelDeviceTasks(id uuid.UUID) []chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	var dones []chan struct{}
	for i := range m.tasks {
		if m.tasks[i].DeviceID != id || m.tasks[i].State != TaskRunning {
			continue
		}
		if cancel, ok := m.taskCancels[m.tasks[i].ID]; ok {
			cancel()
		}
		if done, ok := m.taskDone[m.tasks[i].ID]; ok {
			dones = append(dones, done)
		}
	}
	return dones
}

func waitDones(dones []chan struct{}) {
	for _, done := range dones {
		<-done
	}
}

// errNoSuchDevice is returned by helpers when the device vanished mid-flight.
var errNoSuchDevice = errors.New("device no longer exists")

// connectDirect wires a device that is reachable over ADB without Tailcat.
func (m *Manager) connectDirect(ctx context.Context, id uuid.UUID, generation uuid.UUID, profile Profile, adbPath string) error {
	serial := strings.TrimSpace(profile.DirectTarget)
	if serial == "" {
		return m.failConnect(id, generation, Error("请填写 ADB 地址（IP:端口 或 USB 序列号）"))
	}
	if strings.Contains(serial, ":") {
		// A networked device has to be attached to the local adb server.
		res, err := m.runner.Run(ctx, Command{
			Path:    adbPath,
			Args:    []string{"connect", serial},
			Timeout: adbConnectTimeout,
		}, nil)
		if err != nil {
			return m.failConnect(id, generation, err)
		}
		if out := strings.TrimSpace(res.Output); out != "" {
			m.log(id, out)
		}
	}

	m.mu.Lock()
	if !m.isCurrentLocked(id, generation) {
		m.mu.Unlock()
		return nil
	}
	m.runtimes[id] = &runtimeState{serial: serial, adb: adbPath, generation: generation}
	m.setStateLocked(id, func(s *Session) {
		s.Serial = serial
	})
	m.logLocked(id, "使用本地 ADB 连接")
	m.notify()
	m.mu.Unlock()

	// Only ADB decides whether the direct target is usable.
	if err := m.validateTargetLocked(ctx, id, serial, adbPath, generation); err != nil {
		return m.failConnect(id, generation, err)
	}
	m.startMonitor(id, generation)
	return nil
}

// validateTargetLocked checks one direct target with adb get-state.
func (m *Manager) validateTargetLocked(ctx context.Context, id uuid.UUID, serial, adbPath string, generation uuid.UUID) error {
	res, err := m.runner.Run(ctx, Command{
		Path:    adbPath,
		Args:    []string{"-s", serial, "get-state"},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil {
		return sanitizeError(err)
	}
	state := StateOffline
	output := strings.TrimSpace(res.Output)
	switch {
	case res.ExitCode == 0 && output == "device":
		state = StateOnline
	case strings.Contains(output, "unauthorized"):
		state = StateUnauthorized
	}
	m.mu.Lock()
	if !m.isCurrentLocked(id, generation) {
		m.mu.Unlock()
		return nil
	}
	m.setStateLocked(id, func(s *Session) {
		s.State = state
		if state == StateOffline {
			s.Err = "ADB 尚未上线"
		}
	})
	m.notify()
	m.mu.Unlock()
	if state == StateOffline {
		return Error("该设备的 ADB 尚未进入 device 状态")
	}
	return nil
}
