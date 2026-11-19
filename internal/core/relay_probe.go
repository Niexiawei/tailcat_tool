package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tailscale.com/net/stun"
	"tailscale.com/net/tlsdial"
	"tailscale.com/tailcfg"
)

// A node probe answers one question the DERP page could not: does this node
// still work, right now, from this machine? It asks twice, because the two
// answers mean different things — the HTTPS check says the relay is reachable
// (which is what a device connects through), and the STUN check says the node
// answers over UDP (which is what a direct path needs).
//
// Both checks report failures as data: one dead node is not a broken cycle.
const (
	// relayProbeTimeout bounds each check, close to netcheck's stunProbeTimeout.
	relayProbeTimeout = 3 * time.Second
	// relayProbeBodyLimit is how much of the reply is read. The status is the
	// answer; a huge body must not be buffered.
	relayProbeBodyLimit = 8 << 10
	// relayProbeDefaultTLSPort and relayProbeDefaultSTUNPort are tailcfg's zero
	// values: DERPPort 0 means 443, STUNPort 0 means 3478.
	relayProbeDefaultTLSPort  = 443
	relayProbeDefaultSTUNPort = 3478
	// relayProbeLatencyPath is the endpoint derper serves for exactly this
	// purpose, and what netcheck asks for.
	relayProbeLatencyPath = "/derp/latency-check"
)

// The notes a failed check carries. They are what the UI shows in a tooltip,
// so they are short, in the user's language, and never echo a URL or a body.
const (
	relayProbeNoteTimeout = "超时"
	relayProbeNoteDial    = "连接失败"
	relayProbeNoteNoSTUN  = "不是 STUN 响应"
)

// RelayProbeTarget is one node as a probe needs it. The fields are the tailcfg
// ones that change where a probe goes: DERPPort is the TLS port (443 when
// zero), CertName is the TLS name to expect (HostName when empty, or a
// sha256-raw: pin), STUNPort is 3478 when zero and negative when the node has
// none.
type RelayProbeTarget struct {
	RegionID tailcfg.DERPRegionID
	Name     string
	HostName string
	CertName string
	DERPPort int
	STUNPort int
	STUNOnly bool
}

// RelayCheck is one protocol's outcome. A check that could not run (an
// STUN-only node has no relay; a node without a STUN port has no UDP endpoint)
// says so with Ran=false instead of pretending to fail.
type RelayCheck struct {
	Ran    bool
	OK     bool
	Note   string
	Millis int
}

// RelayNodeProbe is both checks on one node.
type RelayNodeProbe struct {
	RegionID  tailcfg.DERPRegionID
	Name      string
	Address   string
	STUNOnly  bool
	HTTP      RelayCheck
	STUN      RelayCheck
	CheckedAt time.Time
}

// Available reports whether tailcat could use this node: the relay channel is
// what a device connects through, so a STUN-only node is judged by STUN and
// everything else by the HTTPS check. A blocked STUN port says nothing about
// the relay.
func (p RelayNodeProbe) Available() bool {
	if p.STUNOnly {
		return p.STUN.Ran && p.STUN.OK
	}
	return p.HTTP.Ran && p.HTTP.OK
}

// NodeProber runs the two checks on one node. Tests inject a fake so no packet
// leaves the machine.
type NodeProber interface {
	ProbeNode(ctx context.Context, target RelayProbeTarget) (RelayCheck, RelayCheck)
}

// RelayProber is the real prober.
type RelayProber struct {
	// Client runs the HTTPS check as-is. Tests set it to the httptest server's
	// own client, which trusts the test certificate; nil builds one client per
	// node, because the TLS configuration differs per node.
	Client *http.Client
	// Endpoint builds the HTTPS check's URL from one target; nil uses
	// probeURL.
	Endpoint func(target RelayProbeTarget) string
	// Timeout bounds each check; zero means relayProbeTimeout.
	Timeout time.Duration
}

// ProbeNode asks a node both questions, one after the other. Running them in
// parallel would save a few hundred milliseconds per node, but a cycle probes
// eight nodes at a time already; the simpler order is easier to reason about
// when a node answers one and not the other.
func (p *RelayProber) ProbeNode(ctx context.Context, target RelayProbeTarget) (RelayCheck, RelayCheck) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = relayProbeTimeout
	}
	return p.probeHTTP(ctx, target, timeout), p.probeSTUN(ctx, target, timeout)
}

// probeHTTP asks for the latency-check page over TLS. Any 2xx means the relay
// is there; a proxy's error page means it is not.
func (p *RelayProber) probeHTTP(ctx context.Context, target RelayProbeTarget, timeout time.Duration) RelayCheck {
	if target.STUNOnly {
		// The node serves no relay at all, so there is nothing to ask.
		return RelayCheck{Ran: false}
	}
	endpoint := probeURL(target)
	if p.Endpoint != nil {
		endpoint = p.Endpoint(target)
	}
	client := p.Client
	if client == nil {
		client = relayProbeClient(target)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return RelayCheck{Ran: true, Note: relayProbeNoteDial}
	}
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return RelayCheck{Ran: true, Note: relayProbeNote(err)}
	}
	defer response.Body.Close()
	check := RelayCheck{Ran: true, Millis: int(time.Since(start).Milliseconds())}
	// Read a little and stop: the status is the answer, and a body that breaks
	// off must not turn a working relay into a failure.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, relayProbeBodyLimit))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		check.Note = fmt.Sprintf("服务器返回 %d", response.StatusCode)
		return check
	}
	check.OK = true
	return check
}

// probeSTUN sends one binding request and waits for the reply that carries the
// same transaction ID. Packets from anything else are noise and are ignored.
func (p *RelayProber) probeSTUN(ctx context.Context, target RelayProbeTarget, timeout time.Duration) RelayCheck {
	address, ok := stunAddress(target)
	if !ok {
		// STUNPort -1: the map says this node has no STUN endpoint.
		return RelayCheck{Ran: false}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", address)
	if err != nil {
		return RelayCheck{Ran: true, Note: relayProbeNote(err)}
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	txID := stun.NewTxID()
	start := time.Now()
	if _, err := conn.Write(stun.Request(txID)); err != nil {
		return RelayCheck{Ran: true, Note: relayProbeNote(err)}
	}
	buffer := make([]byte, 64<<10)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			return RelayCheck{Ran: true, Note: relayProbeNote(err)}
		}
		packet := buffer[:n]
		if !stun.Is(packet) {
			continue
		}
		reply, _, err := stun.ParseResponse(packet)
		if err != nil {
			return RelayCheck{Ran: true, Note: relayProbeNoteNoSTUN}
		}
		if reply != txID {
			continue
		}
		return RelayCheck{Ran: true, OK: true, Millis: int(time.Since(start).Milliseconds())}
	}
}

// probeURL is where the HTTPS check lands. DERPPort is the alternate TLS port
// (443 when zero), not a plaintext port: DERP clients always speak TLS.
func probeURL(target RelayProbeTarget) string {
	port := target.DERPPort
	if port <= 0 {
		port = relayProbeDefaultTLSPort
	}
	return "https://" + net.JoinHostPort(target.HostName, strconv.Itoa(port)) + relayProbeLatencyPath
}

// relayAddress is the node's relay address as a tooltip shows it.
func relayAddress(target RelayProbeTarget) string {
	port := target.DERPPort
	if port <= 0 {
		port = relayProbeDefaultTLSPort
	}
	return net.JoinHostPort(target.HostName, strconv.Itoa(port))
}

// stunAddress is where the UDP check lands; zero means 3478 and -1 means the
// node has no STUN endpoint.
func stunAddress(target RelayProbeTarget) (string, bool) {
	port := target.STUNPort
	if port < 0 {
		return "", false
	}
	if port == 0 {
		port = relayProbeDefaultSTUNPort
	}
	return net.JoinHostPort(target.HostName, strconv.Itoa(port)), true
}

// probeTLSConfig mirrors derphttp.Client.tlsClient: the handshake names the
// host we dial, while CertName names the certificate to expect. A
// sha256-raw: CertName pins the certificate itself. TLS verification stays on;
// InsecureForTests is a tailscale test hook and is deliberately not honored.
//
// The plain path is Go's own verification against the system roots. It does not
// start from tlsdial.Config: that installs tailscale's baked-root hook for old
// Android devices, and this is a desktop app that should verify the way its
// other HTTP requests do.
func probeTLSConfig(target RelayProbeTarget) *tls.Config {
	conf := &tls.Config{ServerName: target.HostName}
	if target.CertName == "" {
		return conf
	}
	if pin, ok := strings.CutPrefix(target.CertName, "sha256-raw:"); ok {
		tlsdial.SetConfigExpectedCertHash(conf, pin)
		return conf
	}
	tlsdial.SetConfigExpectedCert(conf, target.CertName)
	return conf
}

// relayProbeClient is the HTTPS client for one node. It cannot be shared: the
// TLS configuration is built from the node's own CertName.
func relayProbeClient(target RelayProbeTarget) *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{
		// The proxy comes from the environment, as every other request of the
		// app does: a corporate network that needs one must still be able to
		// answer "is this node usable".
		Proxy: http.ProxyFromEnvironment,
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			tlsConn := tls.Client(conn, probeTLSConfig(target))
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}}
}

// relayProbeNote turns a transport failure into the words a tooltip shows.
func relayProbeNote(err error) string {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return relayProbeNoteTimeout
	}
	return relayProbeNoteDial
}
