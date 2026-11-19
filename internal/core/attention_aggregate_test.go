package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- the pure rule -------------------------------------------------------

func TestAggregateKeepsOneDeviceAsSerialDetached(t *testing.T) {
	at := time.Now()
	groups := AggregateDetachments([]Detachment{
		{DeviceID: uuid.New(), Serial: "127.0.0.1:5556", At: at},
	}, 3*time.Second)
	if len(groups) != 1 {
		t.Fatalf("AggregateDetachments() = %d groups, want 1", len(groups))
	}
	if len(groups[0].DeviceIDs) != 1 || len(groups[0].Serials) != 1 {
		t.Fatalf("group = %+v, want the one device", groups[0])
	}
}

// "adb kill-server" drops every device at once: one prompt, not one per
// device. The pure rule says which detachments belong together.
func TestAggregateMergesDetachmentsWithinWindow(t *testing.T) {
	first := time.Now()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	groups := AggregateDetachments([]Detachment{
		{DeviceID: ids[0], Serial: "127.0.0.1:5556", At: first},
		{DeviceID: ids[1], Serial: "127.0.0.1:5557", At: first.Add(300 * time.Millisecond)},
		{DeviceID: ids[2], Serial: "127.0.0.1:5558", At: first.Add(2 * time.Second)},
	}, 3*time.Second)
	if len(groups) != 1 {
		t.Fatalf("AggregateDetachments() = %d groups, want 1: %+v", len(groups), groups)
	}
	if len(groups[0].DeviceIDs) != 3 {
		t.Fatalf("group holds %d devices, want 3: %+v", len(groups[0].DeviceIDs), groups[0])
	}
	for i, id := range ids {
		if groups[0].DeviceIDs[i] != id {
			t.Fatalf("group order = %v, want the order the detachments happened", groups[0].DeviceIDs)
		}
	}
}

func TestAggregateIgnoresDetachmentsBeyondWindow(t *testing.T) {
	first := time.Now()
	groups := AggregateDetachments([]Detachment{
		{DeviceID: uuid.New(), Serial: "127.0.0.1:5556", At: first},
		{DeviceID: uuid.New(), Serial: "127.0.0.1:5557", At: first.Add(5 * time.Second)},
	}, 3*time.Second)
	if len(groups) != 2 {
		t.Fatalf("AggregateDetachments() = %d groups, want 2: %+v", len(groups), groups)
	}
}

// Events arrive in whatever order the per-device tickers happen to run in.
func TestAggregateSortsUnorderedDetachments(t *testing.T) {
	first := time.Now()
	groups := AggregateDetachments([]Detachment{
		{DeviceID: uuid.New(), Serial: "127.0.0.1:5556", At: first.Add(900 * time.Millisecond)},
		{DeviceID: uuid.New(), Serial: "127.0.0.1:5557", At: first},
	}, 3*time.Second)
	if len(groups) != 1 {
		t.Fatalf("AggregateDetachments() = %d groups, want 1: %+v", len(groups), groups)
	}
	if groups[0].At != first {
		t.Fatalf("group starts at %v, want the earliest event %v", groups[0].At, first)
	}
}

// --- the manager ---------------------------------------------------------

// fleetFixture is a manager with count connected devices sharing one fake adb.
type fleetFixture struct {
	m       *Manager
	runner  *fakeRunner
	id      []uuid.UUID
	adbDown bool
}

func newFleet(t *testing.T, count int) *fleetFixture {
	t.Helper()
	creds := NewMemoryCredentialStore()
	profiles := make([]Profile, count)
	ids := make([]uuid.UUID, count)
	for i := range profiles {
		ids[i] = uuid.New()
		profiles[i] = Profile{ID: ids[i], Name: string(rune('A' + i)), RemotePort: 5555}
		if err := creds.Write(sampleAddress, ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	f := &fleetFixture{id: ids}
	f.runner = &fakeRunner{handler: func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			if f.adbDown {
				return Result{ExitCode: 1, Output: "error: device offline\n"}, nil
			}
			return Result{Output: "device\n"}, nil
		}
		if contains(c.Args, "devices") {
			if f.adbDown {
				// "adb kill-server" and then nothing: every entry is gone.
				return Result{Output: "List of devices attached\n"}, nil
			}
			return Result{}, nil
		}
		return Result{}, nil
	}}
	f.m = NewManager(ManagerOptions{
		Configuration:   Configuration{Profiles: profiles, Tools: ToolSettings{ADB: os.Args[0]}},
		Credentials:     creds,
		Identities:      NewIdentityStore(t.TempDir()),
		Runner:          f.runner,
		Forwarder:       &fakeOpener{},
		Locate:          func(string) (string, bool) { return "", false },
		MonitorInterval: time.Hour, // the test drives the detection itself
	})
	t.Cleanup(func() { f.m.Shutdown(context.Background()) })
	for _, id := range ids {
		if err := f.m.Connect(context.Background(), id); err != nil {
			t.Fatalf("Connect() error: %v", err)
		}
	}
	return f
}

// detect runs the interference rule for one device, the way its monitor tick
// would, without waiting for a ticker.
func (f *fleetFixture) detect(t *testing.T, id uuid.UUID) {
	t.Helper()
	f.m.mu.Lock()
	generation := f.m.generations[id]
	f.m.mu.Unlock()
	f.m.detectInterference(context.Background(), id, generation)
}

func TestAggregateMarksEveryAffectedDeviceNeedsAction(t *testing.T) {
	f := newFleet(t, 3)
	f.adbDown = true
	for _, id := range f.id {
		f.detect(t, id)
	}

	attentions := f.m.Attentions()
	if len(attentions) != 1 {
		t.Fatalf("Attentions() = %d entries, want one for the whole fleet: %+v", len(attentions), attentions)
	}
	if attentions[0].Kind != AttentionServerRestart {
		t.Fatalf("kind = %q, want %q", attentions[0].Kind, AttentionServerRestart)
	}
	if len(attentions[0].DeviceIDs) != 3 {
		t.Fatalf("aggregated attention holds %d devices, want 3: %+v", len(attentions[0].DeviceIDs), attentions[0])
	}
	// The one red line: every affected device must already be waiting for the
	// user, before any retry can connect it back.
	for _, id := range f.id {
		session := f.m.Session(id)
		if session.State != StateNeedsAction {
			t.Fatalf("device %v is %v, want needsAction", id, session.State)
		}
	}
	if connects := f.connectsAfter(f.adbDown); connects != 0 {
		t.Fatalf("adb connect ran %d times after the detachments, want none", connects)
	}
}

// connectsAfter counts "connect" commands beyond the ones Connect made while
// the fleet was set up.
func (f *fleetFixture) connectsAfter(_ bool) int {
	count := 0
	for _, line := range f.runner.commandLog() {
		if strings.Contains(line, " connect ") {
			count++
		}
	}
	return count - len(f.id)
}

func TestAckAttentionClearsEveryDeviceInTheGroup(t *testing.T) {
	f := newFleet(t, 3)
	f.adbDown = true
	for _, id := range f.id {
		f.detect(t, id)
	}
	// Acknowledging through any member of the group dismisses the whole entry:
	// otherwise the dialog reappears for the second device it was already
	// answered for.
	f.m.AckAttention(f.id[1])
	if attentions := f.m.Attentions(); len(attentions) != 0 {
		t.Fatalf("Attentions() = %+v after acknowledging one member", attentions)
	}
	// The devices stay in "needs action": dismissing the dialog is not a
	// decision to reconnect.
	for _, id := range f.id {
		if state := f.m.Session(id).State; state != StateNeedsAction {
			t.Fatalf("device %v is %v after the acknowledgement, want needsAction", id, state)
		}
	}
}

func TestReconnectAllSkipsHealthyDevices(t *testing.T) {
	f := newFleet(t, 2)
	// One device reports itself online, the other is waiting for a decision.
	f.m.mu.Lock()
	f.m.setStateLocked(f.id[0], func(s *Session) { s.State = StateOnline })
	f.m.setStateLocked(f.id[1], func(s *Session) { s.State = StateNeedsAction })
	f.m.mu.Unlock()

	before := f.connectsAfter(false)
	if err := f.m.ReconnectAll(context.Background(), f.id); err != nil {
		t.Fatalf("ReconnectAll() error: %v", err)
	}
	after := f.connectsAfter(false)
	if after-before != 1 {
		t.Fatalf("ReconnectAll() connected %d devices, want only the one waiting for a decision", after-before)
	}
	if state := f.m.Session(f.id[1]).State; state != StateOnline {
		t.Fatalf("the waiting device is %v, want online", state)
	}
}
