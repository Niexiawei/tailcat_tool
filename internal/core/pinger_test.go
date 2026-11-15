package core

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

type fakeIdentityProvider struct {
	mu    sync.Mutex
	names []string
}

func (f *fakeIdentityProvider) Identity(_ context.Context, name string) (Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	return Identity{File: "/tmp/" + name + ".private.json", PublicKey: "nodekey:" + repeatHex(64)}, nil
}

func (f *fakeIdentityProvider) NodeKey(_ context.Context, name string) (key.NodePrivate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	return key.NewNode(), nil
}

func repeatHex(n int) string { return string(make([]byte, 0)) + hexString(n) }

func hexString(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

type fakePingSession struct {
	onPing  func()
	latency time.Duration
	result  PingResult
	err     error
	closed  atomic.Int32
	block   chan struct{}
}

func (s *fakePingSession) DiscoPing(ctx context.Context) (PingResult, error) {
	if s.onPing != nil {
		s.onPing()
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return PingResult{}, ctx.Err()
		}
	}
	if s.err != nil {
		return PingResult{}, s.err
	}
	res := s.result
	if res.Latency == 0 {
		res.Latency = s.latency
	}
	return res, nil
}

func (s *fakePingSession) Close() error {
	s.closed.Add(1)
	return nil
}

type fakePingDialer struct {
	mu       sync.Mutex
	sessions []*fakePingSession
	newFunc  func() *fakePingSession
	address  []string
}

func (d *fakePingDialer) New(_ context.Context, address string, _ key.NodePrivate, _ RelayConfig, _ func(string, ...any)) (PingSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.address = append(d.address, address)
	s := d.newFunc()
	d.sessions = append(d.sessions, s)
	return s, nil
}

func TestPingUsesProbeIdentity(t *testing.T) {
	ids := &fakeIdentityProvider{}
	dialer := &fakePingDialer{newFunc: func() *fakePingSession { return &fakePingSession{latency: 10 * time.Millisecond} }}
	p := &Pinger{Identities: ids, Dialer: dialer}
	deviceID := uuid.New()
	if _, err := p.Ping(context.Background(), deviceID, sampleAddress, 5, RelayConfig{}); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	ids.mu.Lock()
	defer ids.mu.Unlock()
	want := strings.ToLower(deviceID.String()) + "-probe"
	found := false
	for _, name := range ids.names {
		if name == want {
			found = true
		}
		if name == strings.ToLower(deviceID.String())+"-connection" {
			t.Fatalf("a probe must not use the connection identity: %v", ids.names)
		}
	}
	if !found {
		t.Fatalf("identity names = %v, want %q", ids.names, want)
	}
}

func TestPingSerializesSameDevice(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	dialer := &fakePingDialer{newFunc: func() *fakePingSession {
		return &fakePingSession{latency: 5 * time.Millisecond, onPing: func() {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			inFlight.Add(-1)
		}}
	}}
	p := &Pinger{Identities: &fakeIdentityProvider{}, Dialer: dialer}
	deviceID := uuid.New()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Ping(context.Background(), deviceID, sampleAddress, 5, RelayConfig{}); err != nil {
				t.Errorf("Ping() error: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent probes for one device = %d, want 1", got)
	}
}

func TestPingDifferentDevicesConcurrent(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	dialer := &fakePingDialer{newFunc: func() *fakePingSession {
		return &fakePingSession{latency: 5 * time.Millisecond, onPing: func() {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
			inFlight.Add(-1)
		}}
	}}
	p := &Pinger{Identities: &fakeIdentityProvider{}, Dialer: dialer}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Ping(context.Background(), uuid.New(), sampleAddress, 5, RelayConfig{}); err != nil {
				t.Errorf("Ping() error: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("max concurrent probes across devices = %d, want at least 2", got)
	}
}

func TestPingTimeout(t *testing.T) {
	dialer := &fakePingDialer{newFunc: func() *fakePingSession {
		return &fakePingSession{block: make(chan struct{})}
	}}
	p := &Pinger{Identities: &fakeIdentityProvider{}, Dialer: dialer}
	start := time.Now()
	_, err := p.Ping(context.Background(), uuid.New(), sampleAddress, 1, RelayConfig{})
	if err == nil {
		t.Fatal("Ping() returned no error although the probe never answered")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("probe took %v, want ~1s", elapsed)
	}
}

func TestPingClosesSession(t *testing.T) {
	session := &fakePingSession{latency: time.Millisecond}
	p := &Pinger{Identities: &fakeIdentityProvider{}, Dialer: &fakePingDialer{newFunc: func() *fakePingSession { return session }}}
	if _, err := p.Ping(context.Background(), uuid.New(), sampleAddress, 5, RelayConfig{}); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if session.closed.Load() != 1 {
		t.Fatalf("session closed %d times, want 1", session.closed.Load())
	}
}

func TestRouteMapping(t *testing.T) {
	cases := []struct {
		name     string
		direct   bool
		route    string
		endpoint string
		code     string
		regionID int
	}{
		{"direct", true, "直连 · 203.0.113.7:41641", "203.0.113.7:41641", "", 0},
		{"relay with code", false, "中继 · DERP(sfo)", "", "sfo", 9},
		{"relay without code", false, "中继 · DERP(12)", "", "", 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct, route := relayRoute(tc.endpoint, tc.code, tc.regionID)
			if direct != tc.direct || route != tc.route {
				t.Fatalf("relayRoute() = (%v, %q), want (%v, %q)", direct, route, tc.direct, tc.route)
			}
		})
	}
}

func TestPingResultLatency(t *testing.T) {
	session := &fakePingSession{result: PingResult{Latency: 18 * time.Millisecond, Direct: true, Route: "直连 · 1.2.3.4:41641"}}
	p := &Pinger{Identities: &fakeIdentityProvider{}, Dialer: &fakePingDialer{newFunc: func() *fakePingSession { return session }}}
	out, err := p.Ping(context.Background(), uuid.New(), sampleAddress, 5, RelayConfig{})
	if err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if out.Latency != 18*time.Millisecond || !out.Direct || out.Route != "直连 · 1.2.3.4:41641" {
		t.Fatalf("outcome = %+v", out)
	}
}

var _ IdentityProvider = (*IdentityStore)(nil)
var _ = tailcat.NewPrivateKey
