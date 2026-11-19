package core

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests talk to a real adb server and real devices, so they are skipped
// unless the environment names a target:
//
//	TAILCAT_LIVE_ADB_USB=emulator-5554     go test ./internal/core -run TestLive -v
//	TAILCAT_LIVE_ADB_HOST=127.0.0.1:5555   go test ./internal/core -run TestLive -v
//
// They exist because the automated tests replace adb with a fake: whether the
// real links work (a USB serial, `adb connect` to a networked device, and what
// "adb disconnect" does to a running session) is exactly what the fake cannot
// answer. Nothing here is in CI; the results are recorded in
// docs/verification.md.

// liveManager wires a manager to the real adb on PATH, with the device under
// test as its only profile.
func liveManager(t *testing.T, profile Profile, monitor time.Duration) (*Manager, uuid.UUID) {
	t.Helper()
	adb, err := exec.LookPath("adb")
	if err != nil {
		t.Skip("adb is not on PATH")
	}
	m := NewManager(ManagerOptions{
		Configuration: Configuration{
			Profiles: []Profile{profile},
			Tools:    ToolSettings{ADB: adb},
		},
		Credentials:         NewMemoryCredentialStore(),
		Identities:          NewIdentityStore(t.TempDir()),
		Runner:              NewExecRunner(),
		Locate:              func(string) (string, bool) { return "", false },
		MonitorInterval:     monitor,
		ConnectPollInterval: 200 * time.Millisecond,
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return m, profile.ID
}

func liveDevice(t *testing.T, kind string, monitor time.Duration) (*Manager, uuid.UUID) {
	t.Helper()
	value := os.Getenv("TAILCAT_LIVE_ADB_" + kind)
	if value == "" {
		t.Skipf("TAILCAT_LIVE_ADB_%s is not set", kind)
	}
	return liveManager(t, Profile{
		ID:           uuid.New(),
		Name:         "live " + value,
		RemotePort:   5555,
		DirectADB:    true,
		DirectTarget: value,
	}, monitor)
}

// A USB serial (an emulator counts) connects without going through adb
// connect, and the session knows the serial it got.
func TestLiveDirectUSBSerial(t *testing.T) {
	m, id := liveDevice(t, "USB", time.Hour)
	if err := m.Connect(context.Background(), id); err != nil {
		t.Fatalf("Connect(%s) error: %v", m.Profiles()[0].DirectTarget, err)
	}
	session := m.Session(id)
	if session.State != StateOnline {
		t.Fatalf("state = %v (%s), want online", session.State, session.Err)
	}
	if session.Serial != m.Profiles()[0].DirectTarget {
		t.Fatalf("serial = %q, want the direct target", session.Serial)
	}
	t.Logf("USB %s: state=%v serial=%q Android=%q", session.Serial, session.State, session.Serial, session.Android)
	m.Disconnect(context.Background(), id)
}

// A networked device goes through adb connect first.
func TestLiveDirectNetworkAddress(t *testing.T) {
	m, id := liveDevice(t, "HOST", time.Hour)
	if err := m.Connect(context.Background(), id); err != nil {
		t.Fatalf("Connect(%s) error: %v", m.Profiles()[0].DirectTarget, err)
	}
	session := m.Session(id)
	if session.State != StateOnline {
		t.Fatalf("state = %v (%s), want online", session.State, session.Err)
	}
	if session.Serial != m.Profiles()[0].DirectTarget {
		t.Fatalf("serial = %q, want the direct target", session.Serial)
	}
	t.Logf("HOST %s: state=%v Android=%q", session.Serial, session.State, session.Android)
	m.Disconnect(context.Background(), id)
}

// "adb disconnect" from a terminal must stop the app from reconnecting on its
// own and raise the interference prompt instead — the red line that only a real
// adb server can exercise.
func TestLiveExternalDisconnectStopsRetry(t *testing.T) {
	m, id := liveDevice(t, "HOST", 200*time.Millisecond)
	target := m.Profiles()[0].DirectTarget
	adb, _ := exec.LookPath("adb")

	if err := m.Connect(context.Background(), id); err != nil {
		t.Fatalf("Connect(%s) error: %v", target, err)
	}
	if state := m.Session(id).State; state != StateOnline {
		t.Fatalf("state = %v before the test", state)
	}

	// Put the device back the way it was: this machine's adb server is not a
	// test fixture.
	t.Cleanup(func() { _ = exec.Command(adb, "connect", target).Run() })

	if out, err := exec.Command(adb, "disconnect", target).CombinedOutput(); err != nil {
		t.Fatalf("adb disconnect %s: %v (%s)", target, err, out)
	}

	deadline := time.Now().Add(15 * time.Second)
	for len(m.Attentions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	attentions := m.Attentions()
	if len(attentions) == 0 {
		t.Fatalf("no attention after adb disconnect (%s); state = %v", target, m.Session(id).State)
	}
	if attentions[0].Kind != AttentionSerialDetached {
		t.Fatalf("kind = %q, want %q", attentions[0].Kind, AttentionSerialDetached)
	}
	if state := m.Session(id).State; state != StateNeedsAction {
		t.Fatalf("state = %v, want needsAction", state)
	}
	m.mu.Lock()
	retries := m.retryCounts[id]
	m.mu.Unlock()
	if retries != 0 {
		t.Fatalf("retry count = %d: the app tried to connect back on its own", retries)
	}
	t.Logf("attention: %+v", attentions[0])
}
