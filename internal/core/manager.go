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
	// adbRoundTripTimeout bounds one end-to-end check: a healthy device answers
	// in tens of milliseconds, and a silent tunnel is what the timeout is for.
	adbRoundTripTimeout = 4 * time.Second
	// tunnelMissesBeforeStalled is how many tunnel probes in a row must go
	// unanswered before the channel is called quiet: one lost disco packet is
	// packet loss, two in a row is a dead far end.
	tunnelMissesBeforeStalled = 2
	// tunnelPingTimeout bounds one tunnel round trip. It is half the probe
	// interval so a stalled probe cannot queue up behind the next tick.
	tunnelPingTimeout     = 2 * time.Second
	defaultTunnelInterval = 2 * time.Second
)

// ChannelOpener opens device channels (the forwarder in production).
type ChannelOpener interface {
	Open(ctx context.Context, req ForwardRequest, onLog func(string)) (*ForwardConn, error)
}

// Manager owns every device channel. All methods are safe for concurrent use.
type Manager struct {
	mu sync.Mutex
	// listeners are the Change channels handed out, one per caller: a single
	// channel with several readers gives each change to just one of them.
	changedListeners map[chan struct{}]struct{}
	changedMu        sync.Mutex

	config      *ConfigStore
	credentials CredentialStore
	identities  *IdentityStore
	runner      Runner
	forwarder   ChannelOpener
	locate      LocateTool

	profiles        []Profile
	temporary       []Profile
	tempCredentials map[uuid.UUID]string
	// logs carries channel log records to the recorder off the manager lock,
	// so a slow history store can never freeze the window.
	logs    chan LogRecord
	appLogs chan AppLogRecord
	// logcats holds at most one streaming logcat per device.
	logcats map[uuid.UUID]*LogcatStream
	// processNames is each device's PID → process name, as of the last `ps`
	// read; the log page shows it under the tag.
	processNames map[uuid.UUID]map[int]string
	sessions     map[uuid.UUID]Session
	tasks        []Task
	relayPresets []RelayPreset
	relayHistory []RelayHistoryEntry
	// relayPreviews holds the last fetched map of each source. It is memory
	// only on purpose: a map changes, and a stale copy on disk would look
	// current.
	relayPreviews map[string]RelayMapPreview
	// relayProbes is each source's last node probe, keyed by canonical source
	// (an empty key is the default node list). Memory only, for the same
	// reason: a node that answers today may not tomorrow.
	relayProbes        map[string]RelayProbe
	relayProbeRunning  bool
	relayNodeProber    NodeProber
	relayProbeInterval time.Duration
	// defaultNodeListURL is where the default node list is fetched from. It is
	// a field, not the constant, so a test can serve its own copy.
	defaultNodeListURL string
	tools              ToolSettings

	generations map[uuid.UUID]uuid.UUID
	runtimes    map[uuid.UUID]*runtimeState
	monitors    map[uuid.UUID]context.CancelFunc
	retryCounts map[uuid.UUID]int
	nextRetries map[uuid.UUID]time.Time
	taskCancels map[uuid.UUID]context.CancelFunc
	taskDone    map[uuid.UUID]chan struct{}
	attention   map[uuid.UUID]Attention
	// detachments are the recent serial losses, so that a fleet-wide event is
	// one attention instead of one per device. Only the newest window matters.
	detachments []Detachment
	pinger      *Pinger
	recorder    Recorder
	checker     *Checker

	appMeta          AppMetaSource
	appMetaCache     AppMetaCache
	resourceInterval time.Duration
	resources        map[uuid.UUID]ResourceSnapshot
	sampleCounters   map[uuid.UUID]*resourceCounters
	hardware         map[uuid.UUID]*hardwareInfo
	samplers         map[uuid.UUID]context.CancelFunc
	// dirListings caches what each device folder held when it was read; the
	// file page draws from it, and Disconnect drops it with the channel.
	dirListings map[dirKey]DirListing

	notice              string
	nextTemporaryNumber int

	monitorInterval time.Duration
	// tunnelInterval drives the fast liveness loop, which is deliberately
	// shorter than monitorInterval: adb's own state is polled on the slow loop.
	tunnelInterval time.Duration
	connectPoll    time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
}

type runtimeState struct {
	// conn is nil for a direct ADB connection, which has no Tailcat channel.
	conn       *ForwardConn
	serial     string
	adb        string
	generation uuid.UUID
	// tunnelMisses counts consecutive unanswered tunnel probes; the channel is
	// called quiet at tunnelMissesBeforeStalled, and only a probe that answers
	// again resets it.
	tunnelMisses int
	// stalled marks a channel already reported as tunnelLost, so get-state
	// (which answers from a cache) cannot bring it back and retryOffline
	// rebuilds the channel instead of re-adding a dead ADB entry.
	stalled bool
	// rebuildFailed means the rebuild budget is spent: the session keeps saying
	// so instead of falling back to "正在重建通道" on the next tick.
	rebuildFailed bool
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
	// RelayProber runs the two node checks behind the DERP page; nil means the
	// real prober. Tests inject a fake so no packet leaves the machine.
	RelayProber NodeProber
	// RelayProbeInterval is how often every DERP node is probed again. Zero
	// runs no loop at all, which is what tests and --demo want: without it a
	// test run would start sending packets the moment a manager is built.
	RelayProbeInterval time.Duration
	// DefaultNodeListURL replaces the built-in address of the default node
	// list; empty keeps the constant. Tests point it at their own server so a
	// probe cycle never reaches tailcat's real list.
	DefaultNodeListURL string
	// Recorder persists tasks, logs and reachability results; nil keeps
	// everything in memory.
	Recorder Recorder
	Locate   LocateTool
	// AppMeta supplies application names and icons; nil means the resource
	// page shows package names.
	AppMeta AppMetaSource
	// AppMetaCache is the same store seen through the cache page's narrow
	// interface; nil makes the page say the cache is unavailable.
	AppMetaCache AppMetaCache
	// ResourceInterval is how often an open resource page re-reads a device.
	ResourceInterval time.Duration
	// MonitorInterval is how often the slow loop reads adb's own state.
	MonitorInterval time.Duration
	// TunnelInterval is how often the fast loop asks the tunnel for a round
	// trip; zero uses two seconds.
	TunnelInterval time.Duration
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
	tunnelEvery := opts.TunnelInterval
	if tunnelEvery <= 0 {
		tunnelEvery = defaultTunnelInterval
	}
	pollInterval := opts.ConnectPollInterval
	if pollInterval <= 0 {
		pollInterval = connectPollInterval
	}
	resourceInterval := opts.ResourceInterval
	if resourceInterval <= 0 {
		resourceInterval = defaultResourceInterval
	}
	m := &Manager{
		changedListeners:    map[chan struct{}]struct{}{},
		logs:                make(chan LogRecord, 256),
		appLogs:             make(chan AppLogRecord, 256),
		logcats:             map[uuid.UUID]*LogcatStream{},
		processNames:        map[uuid.UUID]map[int]string{},
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
		relayHistory:        opts.Configuration.RelayHistory,
		tempCredentials:     map[uuid.UUID]string{},
		generations:         map[uuid.UUID]uuid.UUID{},
		runtimes:            map[uuid.UUID]*runtimeState{},
		monitors:            map[uuid.UUID]context.CancelFunc{},
		retryCounts:         map[uuid.UUID]int{},
		nextRetries:         map[uuid.UUID]time.Time{},
		taskCancels:         map[uuid.UUID]context.CancelFunc{},
		taskDone:            map[uuid.UUID]chan struct{}{},
		attention:           map[uuid.UUID]Attention{},
		resources:           map[uuid.UUID]ResourceSnapshot{},
		sampleCounters:      map[uuid.UUID]*resourceCounters{},
		hardware:            map[uuid.UUID]*hardwareInfo{},
		dirListings:         map[dirKey]DirListing{},
		samplers:            map[uuid.UUID]context.CancelFunc{},
		nextTemporaryNumber: 1,
		relayProbes:         map[string]RelayProbe{},
		relayNodeProber:     opts.RelayProber,
		relayProbeInterval:  opts.RelayProbeInterval,
		defaultNodeListURL:  DefaultNodeListURL,
		monitorInterval:     interval,
		tunnelInterval:      tunnelEvery,
		resourceInterval:    resourceInterval,
		appMeta:             opts.AppMeta,
		appMetaCache:        opts.AppMetaCache,
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
	m.pinger = &Pinger{Identities: m.identities}
	prober := opts.Prober
	if prober == nil {
		prober = m.pinger
	}
	m.checker = NewChecker(prober).WithRecorder(opts.Recorder)
	if opts.DefaultNodeListURL != "" {
		m.defaultNodeListURL = opts.DefaultNodeListURL
	}
	if m.relayNodeProber == nil {
		m.relayNodeProber = &RelayProber{}
	}
	if m.relayProbeInterval > 0 {
		// One cycle right away: a user who opens the DERP page should see an
		// answer, not "未检测" for the first five minutes.
		go m.relayProbeLoop()
	}
	for _, p := range m.profiles {
		m.sessions[p.ID] = NewSession()
	}
	go m.writeLogs()
	return m
}

// writeLogs hands queued channel logs to the recorder off the manager lock.
func (m *Manager) writeLogs() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case record := <-m.logs:
			_ = m.recorder.RecordLog(record)
		case record := <-m.appLogs:
			_ = m.recorder.RecordAppLog(record)
		}
	}
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

// Changed reports that state moved; the UI redraws on it. Each caller gets a
// channel of its own and keeps it for as long as it listens: the window and the
// tray both follow the manager, and a shared channel would hand every change to
// exactly one of them, leaving the other on a state the app has left.
//
// Signals coalesce (the channel holds one), because a listener reads live state
// when it wakes: two changes in a row need one redraw.
func (m *Manager) Changed() <-chan struct{} {
	m.changedMu.Lock()
	defer m.changedMu.Unlock()
	ch := make(chan struct{}, 1)
	m.changedListeners[ch] = struct{}{}
	return ch
}

func (m *Manager) notify() {
	m.changedMu.Lock()
	defer m.changedMu.Unlock()
	for ch := range m.changedListeners {
		select {
		case ch <- struct{}{}:
		default:
		}
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

// RelayHistory returns the custom DERP sources seen so far, newest first.
func (m *Manager) RelayHistory() []RelayHistoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RelayHistoryEntry(nil), m.relayHistory...)
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
		// Non-blocking: this runs under the manager lock, and the UI reads
		// that lock every frame.
		select {
		case m.logs <- LogRecord{DeviceID: id, At: at, Text: text}:
		default:
		}
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
		// The previous device's identity must not linger while a new
		// connection is being built.
		clearDeviceInfo(s)
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

	if err := m.openTailcatChannel(ctx, id, generation, profile, adbPath, address); err != nil {
		return m.failConnect(id, generation, err)
	}
	m.startMonitor(id, generation)
	// Connecting is the moment the device identity becomes readable; doing it
	// here keeps the overview correct without a manual refresh.
	_ = m.RefreshDeviceInformation(ctx, id)
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

// clearDeviceInfo forgets what only a live connection can know.
func clearDeviceInfo(s *Session) {
	s.Model = ""
	s.Product = ""
	s.Android = ""
	s.DeviceSerial = ""
	s.ShellID = ""
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
		clearDeviceInfo(s)
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
	m.stopResourceSamplingLocked(id)
	generation := uuid.New()
	m.generations[id] = generation
	runtime := m.runtimes[id]
	source := m.appMeta
	delete(m.runtimes, id)
	delete(m.retryCounts, id)
	delete(m.nextRetries, id)
	delete(m.resources, id)
	delete(m.sampleCounters, id)
	delete(m.hardware, id)
	m.forgetDirListingsLocked(id)
	m.forgetProcessNamesLocked(id)
	m.setStateLocked(id, func(s *Session) {
		s.State = StateDisconnecting
	})
	m.notify()
	m.mu.Unlock()

	// Outside the lock: after a reconnect a process that had no APK may be an
	// application the user installed meanwhile, so the failure has to expire
	// together with the channel it was observed on.
	if source != nil {
		source.Forget(deviceKey(id))
	}

	jobs := m.cancelDeviceTasks(id)
	waitDones(jobs)
	// A log stream reads the channel the disconnect is about to close; it has
	// to stop before the channel does, and off the manager lock like the tasks.
	m.StopLogcat(id)

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
			clearDeviceInfo(s)
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
	// Log streams are stopped here and waited for, unlike everywhere else: an
	// app that exits before its adb would leave the process behind.
	m.mu.Lock()
	streams := make([]*LogcatStream, 0, len(m.logcats))
	for id, stream := range m.logcats {
		streams = append(streams, stream)
		delete(m.logcats, id)
	}
	m.mu.Unlock()
	for _, stream := range streams {
		stream.CloseAndWait()
	}
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
		m.stopResourceSamplingLocked(p.ID)
		delete(m.sessions, p.ID)
		delete(m.generations, p.ID)
		delete(m.retryCounts, p.ID)
		delete(m.nextRetries, p.ID)
		delete(m.resources, p.ID)
		delete(m.sampleCounters, p.ID)
		delete(m.hardware, p.ID)
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
	tunnelEvery := m.tunnelInterval
	m.mu.Unlock()

	go m.pollLoop(ctx, id, generation, interval)
	go m.tunnelLoop(ctx, id, generation, tunnelEvery)
}

// pollLoop reads adb's own view of the device on a timer and owns every state
// write for the device: refresh, interference detection, retries, rebuilds.
func (m *Manager) pollLoop(ctx context.Context, id uuid.UUID, generation uuid.UUID, interval time.Duration) {
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
		m.refresh(ctx, id, generation, true)
		m.maybeDetectInterference(ctx, id, generation)
		m.retryOffline(ctx, id, generation)
	}
}

// tunnelLoop asks the tunnel itself whether the far end still answers. It is
// faster than pollLoop on purpose (a dead tunnel cannot be seen through adb's
// cached get-state) and it never writes session state, so a lost disco packet
// cannot flip the UI on its own.
func (m *Manager) tunnelLoop(ctx context.Context, id uuid.UUID, generation uuid.UUID, interval time.Duration) {
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
		m.probeTunnel(ctx, id, generation)
	}
}

// refresh polls adb get-state and updates the session. With roundTrip set it
// also asks the device itself for one shell round trip, because get-state
// answers from adb's cache and keeps saying "device" while the far end is
// silent; only the monitor loop asks for that (see channelVerdict).
//
// It is the single writer of a session's state: the fast tunnel loop only
// counts misses, so the UI cannot oscillate between two writers.
func (m *Manager) refresh(ctx context.Context, id uuid.UUID, generation uuid.UUID, roundTrip bool) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	var adbPath, serial string
	stalled := false
	if runtime != nil {
		adbPath, serial, stalled = runtime.adb, runtime.serial, runtime.stalled
	}
	m.mu.Unlock()
	if runtime == nil {
		return
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    adbPath,
		Args:    []string{"-s", serial, "get-state"},
		Timeout: adbStateTimeout,
	}, nil)
	output := ""
	if err == nil {
		output = strings.TrimSpace(res.Output)
	}
	adbState := StateOffline
	switch {
	case err == nil && res.ExitCode == 0 && output == "device":
		adbState = StateOnline
	case strings.Contains(output, "unauthorized"):
		adbState = StateUnauthorized
	}

	// A stalled channel has nothing to answer with, so its end-to-end check
	// would only spend the full timeout every tick.
	answered := true
	if roundTrip && !stalled && adbState == StateOnline {
		answered = m.adbRoundTrip(ctx, adbPath, serial) == nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	runtime = m.runtimes[id]
	if !m.isCurrentLocked(id, generation) || runtime == nil {
		return
	}
	if runtime.stalled && runtime.tunnelMisses == 0 {
		// The tunnel answers again: a short outage that healed, so the channel
		// is not torn down for it.
		runtime.stalled = false
		runtime.rebuildFailed = false
		m.logLocked(id, "Tailcat 通道已恢复")
	}
	state, message := channelVerdict(adbState, answered, runtime.tunnelMisses, runtime.stalled)
	if runtime.stalled && runtime.rebuildFailed {
		message = MsgTunnelRebuildFailed
	}
	previous := m.sessions[id].State
	m.setStateLocked(id, func(s *Session) {
		s.State = state
		s.Err = message
	})
	if previous != state {
		if state == StateTunnelLost {
			// One line, not two: the state label says less than this sentence.
			m.logLocked(id, message)
		} else {
			m.logLocked(id, state.Label())
		}
	}
	if state == StateTunnelLost && previous != StateTunnelLost {
		runtime.stalled = true
		m.retryCounts[id] = 0
		delete(m.nextRetries, id)
	}
	// get-state keeps claiming the device is there while the tunnel is dead;
	// resetting the rebuild budget on that claim would make it endless.
	if state == StateOnline && !runtime.stalled {
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
	_, pending := m.attentionForLocked(id)
	m.mu.Unlock()
	if pending || state != StateOffline {
		return
	}
	m.detectInterference(ctx, id, generation)
}

// retryOffline recovers a lost device on a bounded, growing delay. An offline
// device needs adb connect again; a dead tunnel needs a whole new channel,
// because adb connect against a dead tunnel only waits out its timeout.
func (m *Manager) retryOffline(ctx context.Context, id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	state := m.sessions[id].State
	count := m.retryCounts[id]
	next, hasNext := m.nextRetries[id]
	runtime := m.runtimes[id]
	// A stalled channel is rebuilt, not re-added: the ADB entry is a ghost.
	rebuild := state == StateTunnelLost
	attempt := false
	if (state == StateOffline || rebuild) && runtime != nil && count < maxOfflineRetries && (!hasNext || !time.Now().Before(next)) {
		m.retryCounts[id] = count + 1
		m.nextRetries[id] = time.Now().Add(retryDelay(count + 1))
		attempt = true
	}
	var adbPath, serial string
	if runtime != nil {
		adbPath, serial = runtime.adb, runtime.serial
	}
	n := count + 1
	m.mu.Unlock()

	if !attempt {
		return
	}
	suffix := "（" + strconv.Itoa(n) + "/" + strconv.Itoa(maxOfflineRetries) + "）"
	if rebuild {
		m.log(id, "正在重建 Tailcat 通道"+suffix)
		// Everything that reads the dead channel stops before it goes, in
		// Disconnect's order.
		waitDones(m.cancelDeviceTasks(id))
		m.StopLogcat(id)
		m.mu.Lock()
		m.stopResourceSamplingLocked(id)
		m.mu.Unlock()
		if err := m.rebuildChannel(ctx, id, generation); err != nil {
			m.log(id, "Tailcat 通道重建失败："+err.Error())
		}
		m.refresh(ctx, id, generation, false)
	} else {
		m.log(id, "尝试恢复 ADB 连接"+suffix)
		_, _ = m.runner.Run(ctx, Command{
			Path:    adbPath,
			Args:    []string{"connect", serial},
			Timeout: 10 * time.Second,
		}, nil)
		m.refresh(ctx, id, generation, false)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isCurrentLocked(id, generation) || n < maxOfflineRetries {
		m.notify()
		return
	}
	current := m.sessions[id].State
	switch {
	case rebuild && current == StateTunnelLost:
		if runtime := m.runtimes[id]; runtime != nil {
			runtime.rebuildFailed = true
		}
		m.setStateLocked(id, func(s *Session) { s.Err = MsgTunnelRebuildFailed })
		m.logLocked(id, MsgTunnelRebuildFailed)
	case !rebuild && current == StateOffline:
		m.setStateLocked(id, func(s *Session) { s.Err = "自动恢复已尝试 6 次，请检查设备后重新连接" })
	}
	m.notify()
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
	_ = m.RefreshDeviceInformation(ctx, id)
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

// RecordAppLog queues one line the app itself produced: a failed action, a
// startup problem. It is safe to call from the UI thread — a full queue drops
// the line and returns, because a log is best effort and a frame is not.
func (m *Manager) RecordAppLog(record AppLogRecord) {
	if m.recorder == nil {
		return
	}
	record.Text = strings.TrimSpace(Redact(record.Text, ""))
	if record.Text == "" {
		return
	}
	if record.Level == "" {
		record.Level = LogInfo
	}
	if record.At.IsZero() {
		record.At = time.Now()
	}
	select {
	case m.appLogs <- record:
	default:
	}
}
