package core

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/types/key"
)

// fakeSession stands in for a tailcat client: dialing returns one end of an
// in-memory pipe whose other end echoes, so the proxy path is real.
type fakeSession struct {
	mu       sync.Mutex
	closed   bool
	closeAt  time.Time
	dials    int
	lastPort uint16
	echo     string
	logf     func(string, ...any)
}

func (f *fakeSession) DialTCPPort(_ context.Context, port uint16) (net.Conn, error) {
	f.mu.Lock()
	f.dials++
	f.lastPort = port
	echo := f.echo
	f.mu.Unlock()

	local, remote := net.Pipe()
	go func() {
		defer remote.Close()
		if echo != "" {
			_, _ = io.Copy(remote, strings.NewReader(echo))
			return
		}
		_, _ = io.Copy(remote, remote)
	}()
	return local, nil
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.closeAt = time.Now()
	}
	return nil
}

type fakeDialer struct {
	session  *fakeSession
	requests []dialRequest
}

type dialRequest struct {
	addr  string
	relay RelayConfig
	key   key.NodePrivate
}

func (d *fakeDialer) New(_ context.Context, addr string, nodeKey key.NodePrivate, relay RelayConfig, logf func(string, ...any)) (TailcatSession, error) {
	d.requests = append(d.requests, dialRequest{addr: addr, relay: relay, key: nodeKey})
	if d.session != nil {
		d.session.logf = logf
	}
	return d.session, nil
}

func newForwarderForTest(t *testing.T, echo string) (*Forwarder, *fakeDialer, *fakeSession) {
	t.Helper()
	session := &fakeSession{echo: echo}
	dialer := &fakeDialer{session: session}
	f := &Forwarder{Dialer: dialer}
	return f, dialer, session
}

func freePortRange(t *testing.T, from, to int) int {
	t.Helper()
	for p := from; p <= to; p++ {
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
			ln.Close()
			continue
		}
		return p
	}
	t.Fatalf("no busy port in %d..%d to test the fallback", from, to)
	return 0
}

func TestForwardAutoPortFrom5556(t *testing.T) {
	// Occupy the first candidate so the forwarder has to move on.
	busy, err := net.Listen("tcp", "127.0.0.1:5556")
	if err != nil {
		t.Skipf("5556 is not available to occupy: %v", err)
	}
	defer busy.Close()

	f, _, _ := newForwarderForTest(t, "")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())
	if conn.Port == 5556 {
		t.Fatalf("Port = 5556 although it was busy")
	}
	if conn.Port < 5557 || conn.Port > 5655 {
		t.Fatalf("Port = %d, want the next candidate", conn.Port)
	}
	if conn.Serial != "127.0.0.1:"+strconv.Itoa(conn.Port) {
		t.Fatalf("Serial = %q, want 127.0.0.1:%d", conn.Serial, conn.Port)
	}
}

func TestForwardPreferredPortBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:5559")
	if err != nil {
		t.Skipf("5559 is not available to occupy: %v", err)
	}
	defer busy.Close()

	f, _, _ := newForwarderForTest(t, "")
	_, err = f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5559, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err == nil {
		t.Fatal("Open() silently ignored a busy preferred port")
	}
	if !strings.Contains(err.Error(), "5559") {
		t.Fatalf("error should name the port: %v", err)
	}
}

func TestForwardPortReadyImmediately(t *testing.T) {
	f, _, _ := newForwarderForTest(t, "")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5556, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())

	// No waiting loop: the returned port must accept a connection right away.
	c, err := net.DialTimeout("tcp", conn.Serial, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("the port is not listening yet: %v", err)
	}
	c.Close()
}

func TestForwardProxiesTraffic(t *testing.T) {
	f, _, session := newForwarderForTest(t, "hello-from-device\n")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5561, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())

	c, err := net.Dial("tcp", conn.Serial)
	if err != nil {
		t.Fatalf("dialing the forwarder: %v", err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len("hello-from-device\n"))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("reading proxied bytes: %v", err)
	}
	if string(buf) != "hello-from-device\n" {
		t.Fatalf("proxied %q", buf)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.dials != 1 {
		t.Fatalf("session dialed %d times, want 1", session.dials)
	}
	if session.lastPort != 5555 {
		t.Fatalf("session dialed port %d, want the remote port 5555", session.lastPort)
	}
}

func TestForwardCloseOrder(t *testing.T) {
	f, _, session := newForwarderForTest(t, "x")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5562, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	// Hold an active proxied connection so Close has to end it too.
	c, err := net.Dial("tcp", conn.Serial)
	if err != nil {
		t.Fatalf("dialing the forwarder: %v", err)
	}
	defer c.Close()
	time.Sleep(50 * time.Millisecond)

	if err := conn.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	session.mu.Lock()
	closed := session.closed
	closeAt := session.closeAt
	session.mu.Unlock()
	if !closed || closeAt.IsZero() {
		t.Fatal("Close() did not close the tailcat session")
	}
	if _, err := net.DialTimeout("tcp", conn.Serial, 300*time.Millisecond); err == nil {
		t.Fatal("the listener still accepts connections after Close()")
	}
	// The proxied connection must end: keep reading until it errors out.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	for i := 0; i < 64; i++ {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
	t.Fatal("the proxied connection survived Close()")
}

func TestForwardPortReleasedAfterClose(t *testing.T) {
	f, _, _ := newForwarderForTest(t, "")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5563, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	if err := conn.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:5563")
	if err != nil {
		t.Fatalf("the port is still held after Close(): %v", err)
	}
	ln.Close()
}

func TestForwardInlineRelayUsesMap(t *testing.T) {
	relay, err := NormalizeRelay(minimalRelayJSON)
	if err != nil {
		t.Fatal(err)
	}
	f, dialer, _ := newForwarderForTest(t, "")
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5564, RemotePort: 5555, NodeKey: key.NewNode(), Relay: relay,
	}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())
	if len(dialer.requests) != 1 {
		t.Fatalf("dialer saw %d requests", len(dialer.requests))
	}
	got := dialer.requests[0].relay
	if got.Kind != RelayInline || got.Map == nil || len(got.Map.Regions) != 1 {
		t.Fatalf("the inline map did not reach the client: %+v", got)
	}
}

func TestForwardRedactsLogs(t *testing.T) {
	f, _, session := newForwarderForTest(t, "")
	var mu sync.Mutex
	var lines []string
	conn, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, PreferredPort: 5565, RemotePort: 5555, NodeKey: key.NewNode(),
	}, func(s string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, s)
	})
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())
	session.mu.Lock()
	logf := session.logf
	session.mu.Unlock()
	if logf == nil {
		t.Fatal("the forwarder never gave the client a logger")
	}
	logf("dialing %s now", sampleAddress)
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "")
	if strings.Contains(joined, sampleAddress) {
		t.Fatalf("a credential reached the log callback: %q", joined)
	}
	if !strings.Contains(joined, MsgRedacted) {
		t.Fatalf("expected the redaction placeholder, got %q", joined)
	}
}

func TestForwardAllPortsBusy(t *testing.T) {
	f, _, _ := newForwarderForTest(t, "")
	var held []net.Listener
	for p := 5556; p <= 5655; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			for _, h := range held {
				h.Close()
			}
			t.Skipf("cannot occupy %d: %v", p, err)
		}
		held = append(held, ln)
	}
	defer func() {
		for _, h := range held {
			h.Close()
		}
	}()
	_, err := f.Open(context.Background(), ForwardRequest{
		Address: sampleAddress, RemotePort: 5555, NodeKey: key.NewNode(),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "5556") {
		t.Fatalf("error = %v, want one naming the exhausted range", err)
	}
}
