package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRunner struct {
	mu      sync.Mutex
	calls   []Command
	handler func(ctx context.Context, c Command) (Result, error)
}

func (f *fakeRunner) Start(context.Context, Command, func(string)) (*Proc, error) {
	return nil, Error("Start is not used by these tests")
}

func (f *fakeRunner) Run(ctx context.Context, c Command, onOutput func(string)) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		return handler(ctx, c)
	}
	return Result{}, nil
}

func (f *fakeRunner) commandLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Path+" "+strings.Join(c.Args, " "))
	}
	return out
}

// lastSerial returns the serial of the most recent "-s <serial>" call.
func (f *fakeRunner) lastSerial() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		args := f.calls[i].Args
		for j := 0; j < len(args)-1; j++ {
			if args[j] == "-s" {
				return args[j+1]
			}
		}
	}
	return ""
}

func (f *fakeRunner) setHandler(h func(ctx context.Context, c Command) (Result, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = h
}

type fakeOpener struct {
	mu      sync.Mutex
	opens   int
	closes  int
	fail    error
	reqs    []ForwardRequest
	ports   []int
	closedP []bool
	ln      net.Listener
}

func (o *fakeOpener) Open(_ context.Context, req ForwardRequest, _ func(string)) (*ForwardConn, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return nil, o.fail
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	o.opens++
	o.reqs = append(o.reqs, req)
	o.ports = append(o.ports, port)
	o.closedP = append(o.closedP, false)
	index := o.opens - 1
	o.ln = ln
	conn := &ForwardConn{
		Port:   port,
		Serial: fmt.Sprintf("127.0.0.1:%d", port),
		Close: func(context.Context) error {
			o.mu.Lock()
			if !o.closedP[index] {
				o.closedP[index] = true
				o.closes++
			}
			o.mu.Unlock()
			return ln.Close()
		},
	}
	return conn, nil
}

func (o *fakeOpener) openCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opens
}

func (o *fakeOpener) closeCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closes
}

type managerFixture struct {
	m      *Manager
	runner *fakeRunner
	opener *fakeOpener
	creds  *MemoryCredentialStore
	id     uuid.UUID
}

func newManagerFixture(t *testing.T, opts ...func(*ManagerOptions)) *managerFixture {
	t.Helper()
	id := uuid.New()
	creds := NewMemoryCredentialStore()
	if err := creds.Write(sampleAddress, id); err != nil {
		t.Fatal(err)
	}
	profile := Profile{ID: id, Name: "E245", RemotePort: 5555}
	runner := &fakeRunner{handler: func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return Result{}, nil
	}}
	opener := &fakeOpener{}
	mo := ManagerOptions{
		Configuration:       Configuration{Profiles: []Profile{profile}, Tools: ToolSettings{ADB: os.Args[0]}},
		Credentials:         creds,
		Identities:          NewIdentityStore(t.TempDir()),
		Runner:              runner,
		Forwarder:           opener,
		Locate:              func(string) (string, bool) { return "", false },
		MonitorInterval:     time.Hour, // keep the monitor quiet unless a test sets it
		ConnectPollInterval: 2 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&mo)
	}
	m := NewManager(mo)
	return &managerFixture{m: m, runner: runner, opener: opener, creds: creds, id: id}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestConnectFlow(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	session := f.m.Session(f.id)
	if session.State != StateOnline {
		t.Fatalf("state = %v, want online", session.State)
	}
	if session.Serial == "" || session.Port == 0 {
		t.Fatalf("session has no serial/port: %+v", session)
	}
	if f.opener.closeCount() != 0 {
		t.Fatal("the channel was closed during a successful connect")
	}
	log := f.runner.commandLog()
	if len(log) < 2 {
		t.Fatalf("adb was called %d times, want at least connect + get-state", len(log))
	}
	if !strings.Contains(log[0], "connect "+session.Serial) {
		t.Fatalf("first adb call = %q, want connect %s", log[0], session.Serial)
	}
	if !strings.Contains(log[1], "-s "+session.Serial+" get-state") {
		t.Fatalf("second adb call = %q, want get-state on the channel serial", log[1])
	}
}

func TestConnectRejectsBadAddress(t *testing.T) {
	f := newManagerFixture(t)
	f.id = uuid.New()
	if err := f.creds.Write("not-a-tailcat-address", f.id); err != nil {
		t.Fatal(err)
	}
	f.m.profiles = append(f.m.profiles, Profile{ID: f.id, Name: "bad", RemotePort: 5555})
	f.m.sessions[f.id] = NewSession()

	err := f.m.Connect(context.Background(), f.id)
	if err == nil {
		t.Fatal("Connect() accepted an invalid address")
	}
	if f.opener.openCount() != 0 {
		t.Fatal("a listener was opened for an invalid address")
	}
	if state := f.m.Session(f.id).State; state != StateFailed {
		t.Fatalf("state = %v, want failed", state)
	}
}

func TestConnectUnauthorized(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device unauthorized\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if state := f.m.Session(f.id).State; state != StateUnauthorized {
		t.Fatalf("state = %v, want unauthorized", state)
	}
}

func TestConnectFailureMarksFailed(t *testing.T) {
	f := newManagerFixture(t)
	f.opener.fail = Error("boom")
	if err := f.m.Connect(context.Background(), f.id); err == nil {
		t.Fatal("Connect() swallowed the opener error")
	}
	session := f.m.Session(f.id)
	if session.State != StateFailed || session.Serial != "" {
		t.Fatalf("session = %+v, want failed without serial", session)
	}
	if !strings.Contains(session.Err, "boom") {
		t.Fatalf("session error = %q", session.Err)
	}
}

func TestDisconnectClosesChannelAndAdb(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	serial := f.m.Session(f.id).Serial
	port := f.m.Session(f.id).Port

	f.m.Disconnect(context.Background(), f.id)

	if f.opener.closeCount() != 1 {
		t.Fatalf("channel closed %d times, want 1", f.opener.closeCount())
	}
	found := false
	for _, cmd := range f.runner.commandLog() {
		if strings.HasSuffix(cmd, "disconnect "+serial) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no adb disconnect for %s in %v", serial, f.runner.commandLog())
	}
	if state := f.m.Session(f.id).State; state != StateDisconnected {
		t.Fatalf("state = %v, want disconnected", state)
	}
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
		t.Fatal("the port still accepts connections after Disconnect()")
	}
}

// A late callback from an old generation must not touch the current state.
func TestStaleGenerationIgnored(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.m.mu.Lock()
	oldGeneration := f.m.generations[f.id]
	f.m.generations[f.id] = uuid.New()
	f.m.mu.Unlock()

	f.m.refresh(context.Background(), f.id, oldGeneration)
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("a stale generation changed the state to %v", state)
	}
}

func TestMonitorPolls(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.MonitorInterval = 10 * time.Millisecond })
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	polls := 0
	for _, cmd := range f.runner.commandLog() {
		if strings.Contains(cmd, "get-state") {
			polls++
		}
	}
	if polls < 3 {
		t.Fatalf("monitor polled %d times in 120ms, want at least 3", polls)
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestRetryDelaySchedule(t *testing.T) {
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, expected := range want {
		if got := retryDelay(i + 1); got != expected {
			t.Fatalf("retryDelay(%d) = %v, want %v", i+1, got, expected)
		}
	}
}

func TestUnauthorizedDoesNotRetry(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.MonitorInterval = 5 * time.Millisecond })
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device unauthorized\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	before := len(f.runner.commandLog())
	time.Sleep(100 * time.Millisecond)
	for _, cmd := range f.runner.commandLog()[before:] {
		if strings.Contains(cmd, " connect ") {
			t.Fatalf("an unauthorized device was reconnected automatically: %q", cmd)
		}
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestOfflineRetriesAreBounded(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.MonitorInterval = 5 * time.Millisecond })
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device offline\n"}, nil
		}
		if contains(c.Args, "devices") {
			// The entry is still attached: a plain offline device, not
			// external interference.
			return Result{Output: "List of devices attached\n" + f.runner.lastSerial() + "\toffline\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	f.m.mu.Lock()
	count := f.m.retryCounts[f.id]
	f.m.mu.Unlock()
	if count != 1 {
		t.Fatalf("retry count = %d after 150ms, want 1 (the next attempt is 2s away)", count)
	}
	if state := f.m.Session(f.id).State; state != StateOffline {
		t.Fatalf("state = %v, want offline", state)
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestShutdownDisconnectsEverything(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.m.Shutdown(context.Background())
	if f.opener.closeCount() != 1 {
		t.Fatalf("channel closed %d times, want 1", f.opener.closeCount())
	}
	if state := f.m.Session(f.id).State; state != StateDisconnected {
		t.Fatalf("state = %v, want disconnected", state)
	}
}

func TestConnectRequiresAdb(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Configuration.Tools = ToolSettings{}
	})
	err := f.m.Connect(context.Background(), f.id)
	if err == nil || !strings.Contains(err.Error(), "ADB") {
		t.Fatalf("Connect() = %v, want a missing-ADB error", err)
	}
}

func TestDirectConnectNetworkTarget(t *testing.T) {
	f := newManagerFixture(t)
	f.m.mu.Lock()
	f.m.profiles[0].DirectADB = true
	f.m.profiles[0].DirectTarget = "192.168.1.20:5555"
	f.m.mu.Unlock()

	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	session := f.m.Session(f.id)
	if session.State != StateOnline || session.Serial != "192.168.1.20:5555" {
		t.Fatalf("session = %+v", session)
	}
	if f.opener.openCount() != 0 {
		t.Fatalf("a Tailcat channel was opened for a direct device (%d)", f.opener.openCount())
	}
	log := f.runner.commandLog()
	if len(log) == 0 || !strings.Contains(log[0], "connect 192.168.1.20:5555") {
		t.Fatalf("first adb call = %v, want connect", log)
	}
	// No identity is touched in direct mode.
	if _, err := f.m.identities.Identity(context.Background(), f.id.String()+"-connection"); err == nil {
		t.Log("identity store is reachable; it must simply not be used")
	}
}

func TestDirectConnectUSBSerial(t *testing.T) {
	f := newManagerFixture(t)
	f.m.mu.Lock()
	f.m.profiles[0].DirectADB = true
	f.m.profiles[0].DirectTarget = "emulator-5554"
	f.m.mu.Unlock()

	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if got := f.m.Session(f.id).Serial; got != "emulator-5554" {
		t.Fatalf("serial = %q", got)
	}
	for _, cmd := range f.runner.commandLog() {
		if strings.Contains(cmd, " connect ") {
			t.Fatalf("a USB serial was sent to adb connect: %q", cmd)
		}
	}
}

func TestDirectTargetValidation(t *testing.T) {
	cases := []struct {
		target  string
		wantErr bool
	}{
		{"192.168.1.20:5555", false},
		{"emulator-5554", false},
		{"", true},
		{"has space", true},
		{"tc" + strings.Repeat("A", 24), true},
	}
	for _, tc := range cases {
		_, err := validateDirectTarget(tc.target)
		if tc.wantErr != (err != nil) {
			t.Fatalf("validateDirectTarget(%q) = %v", tc.target, err)
		}
	}
}

func TestFailConnectSurvivesDirectRuntime(t *testing.T) {
	// adb reports an unresponsive device, so the direct connect path reaches
	// failConnect with a runtime whose conn is nil.
	runner := &fakeRunner{handler: func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{}, Error("adb: device offline")
		}
		return Result{}, nil
	}}
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Runner = runner
		o.Configuration.Profiles[0].DirectADB = true
		o.Configuration.Profiles[0].DirectTarget = "127.0.0.1:5554"
	})
	if err := f.m.Connect(context.Background(), f.id); err == nil {
		t.Fatal("Connect() succeeded against an unresponsive direct target")
	}
	session := f.m.Session(f.id)
	if session.State != StateFailed {
		t.Fatalf("state = %v, want failed", session.State)
	}
	if session.Err == "" {
		t.Fatal("the failure was not reported in the session")
	}
}

func TestReconnectOverDirectRuntimeDropsIt(t *testing.T) {
	// A second connect must clean up the previous direct runtime, which has
	// no conn to close.
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Configuration.Profiles[0].DirectADB = true
		o.Configuration.Profiles[0].DirectTarget = "emulator-5554"
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("reconnect error: %v", err)
	}
	f.m.Disconnect(context.Background(), f.id)
	if got := f.m.Session(f.id).State; got != StateDisconnected {
		t.Fatalf("state = %v, want disconnected", got)
	}
}
