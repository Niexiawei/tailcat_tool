package core

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeProber struct {
	mu        sync.Mutex
	inFlight  int
	maxFlight int
	pinged    []uuid.UUID
	block     bool
	result    PingOutcome
	err       error
}

func (p *fakeProber) Ping(ctx context.Context, id uuid.UUID, _ string, _ int, _ RelayConfig) (PingOutcome, error) {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxFlight {
		p.maxFlight = p.inFlight
	}
	p.pinged = append(p.pinged, id)
	block, result, err := p.block, p.result, p.err
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()
	time.Sleep(30 * time.Millisecond)
	if block {
		<-ctx.Done()
		return PingOutcome{}, ErrCancelled
	}
	return result, err
}

func reachProfiles(n int) []Profile {
	out := make([]Profile, n)
	for i := range out {
		out[i] = Profile{ID: uuid.New(), Name: "dev" + strconv.Itoa(i), RemotePort: 5555}
	}
	return out
}

func runChecker(t *testing.T, c *Checker, profiles []Profile) {
	t.Helper()
	c.Start(profiles, map[uuid.UUID]bool{}, func(uuid.UUID) ConnectionState { return StateOnline },
		func(uuid.UUID) (string, error) { return sampleAddress, nil })
	c.Wait()
}

func TestReachabilityConcurrency3(t *testing.T) {
	prober := &fakeProber{result: PingOutcome{Latency: 18 * time.Millisecond, Direct: true, Route: "直连 · 1.2.3.4:41641"}}
	c := NewChecker(prober)
	runChecker(t, c, reachProfiles(10))
	if got := prober.maxFlight; got != 3 {
		t.Fatalf("peak concurrency = %d, want 3", got)
	}
	if len(prober.pinged) != 10 {
		t.Fatalf("pinged %d devices, want 10", len(prober.pinged))
	}
	for _, r := range c.Results() {
		if r.State != ReachReachable {
			t.Fatalf("result %+v, want reachable", r)
		}
		if r.Latency != "18ms" {
			t.Fatalf("latency = %q, want 18ms", r.Latency)
		}
		if r.ADBState != StateOnline {
			t.Fatalf("adb state = %v, want the snapshot at completion", r.ADBState)
		}
		if r.CheckedAt.IsZero() {
			t.Fatal("result has no check time")
		}
	}
}

func TestReachabilityCancel(t *testing.T) {
	prober := &fakeProber{block: true}
	c := NewChecker(prober)
	c.Start(reachProfiles(10), map[uuid.UUID]bool{}, func(uuid.UUID) ConnectionState { return StateDisconnected },
		func(uuid.UUID) (string, error) { return sampleAddress, nil })
	// Let the first three get going, then cancel.
	deadline := time.Now().Add(time.Second)
	for {
		started := false
		for _, r := range c.Results() {
			if r.State == ReachTesting {
				started = true
			}
		}
		if started || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.Cancel()
	c.Wait()
	cancelled := 0
	for _, r := range c.Results() {
		if r.State == ReachCancelled {
			cancelled++
		}
	}
	if cancelled == 0 {
		t.Fatalf("no result was cancelled: %+v", c.Results())
	}
}

func TestUnreachableVsFailed(t *testing.T) {
	t.Run("timeout is unreachable", func(t *testing.T) {
		prober := &fakeProber{err: ErrProbeTimeout}
		c := NewChecker(prober)
		runChecker(t, c, reachProfiles(1))
		if got := c.Results()[0].State; got != ReachUnreachable {
			t.Fatalf("state = %v, want unreachable", got)
		}
	})
	t.Run("other errors are failures", func(t *testing.T) {
		prober := &fakeProber{err: Error("refused")}
		c := NewChecker(prober)
		runChecker(t, c, reachProfiles(1))
		if got := c.Results()[0].State; got != ReachFailed {
			t.Fatalf("state = %v, want failed", got)
		}
	})
}

func TestDetailTruncated1000(t *testing.T) {
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'y'
	}
	prober := &fakeProber{err: Error(string(long))}
	c := NewChecker(prober)
	runChecker(t, c, reachProfiles(1))
	if n := len(c.Results()[0].Detail); n > 1000 {
		t.Fatalf("detail is %d bytes, want at most 1000", n)
	}
}

func TestReachabilityMissingCredential(t *testing.T) {
	prober := &fakeProber{result: PingOutcome{}}
	c := NewChecker(prober)
	c.Start(reachProfiles(1), map[uuid.UUID]bool{}, func(uuid.UUID) ConnectionState { return StateDisconnected },
		func(uuid.UUID) (string, error) { return "", nil })
	c.Wait()
	res := c.Results()[0]
	if res.State != ReachFailed {
		t.Fatalf("state = %v, want failed", res.State)
	}
	if len(prober.pinged) != 0 {
		t.Fatal("a device without a credential was probed")
	}
}
