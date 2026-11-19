package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"tailscale.com/net/stun"
	"tailscale.com/net/stun/stuntest"
)

// latencyCheckServer stands in for a node's /derp/latency-check: the prober is
// wired to it, so the HTTPS half of a test never leaves the machine.
func latencyCheckServer(t *testing.T, handler http.HandlerFunc) *RelayProber {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return &RelayProber{
		Client:   server.Client(),
		Endpoint: func(RelayProbeTarget) string { return server.URL + "/derp/latency-check" },
		Timeout:  2 * time.Second,
	}
}

// httpOnlyTarget is a node whose UDP half is switched off, for the tests that
// only care about the HTTPS check.
func httpOnlyTarget() RelayProbeTarget {
	return RelayProbeTarget{RegionID: 901, Name: "901a", HostName: "relay.example.com", STUNPort: -1}
}

// TestHTTPProbeAcceptsALatencyCheckReply pins the successful case: a 200 from
// /derp/latency-check is what tailscale's own netcheck treats as "this node's
// relay works", and the report carries no failure note.
func TestHTTPProbeAcceptsALatencyCheckReply(t *testing.T) {
	var path string
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	check, _ := prober.ProbeNode(context.Background(), httpOnlyTarget())
	if !check.Ran {
		t.Fatal("HTTP check did not run")
	}
	if !check.OK {
		t.Fatalf("HTTP check failed on a 200: %+v", check)
	}
	if check.Note != "" {
		t.Fatalf("a passing check carries a note %q", check.Note)
	}
	if path != "/derp/latency-check" {
		t.Fatalf("probe asked for %q, want /derp/latency-check", path)
	}
}

// TestHTTPProbeRejectsAStatusAMiddleboxWouldSend pins the reason the check
// insists on 2xx: a corporate proxy answers with its own page, and a node
// behind it must not read as usable.
func TestHTTPProbeRejectsAStatusAMiddleboxWouldSend(t *testing.T) {
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	check, _ := prober.ProbeNode(context.Background(), httpOnlyTarget())
	if check.OK {
		t.Fatal("a 403 was accepted as a working relay")
	}
	if !strings.Contains(check.Note, "403") {
		t.Fatalf("note %q does not name the status code", check.Note)
	}
}

// TestHTTPProbeGivesUpAtTheTimeout pins the bound: a node that never answers
// must not hold the cycle open until the app is closed.
func TestHTTPProbeGivesUpAtTheTimeout(t *testing.T) {
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	prober.Timeout = 50 * time.Millisecond
	start := time.Now()
	check, _ := prober.ProbeNode(context.Background(), httpOnlyTarget())
	if check.OK {
		t.Fatal("a silent node was accepted")
	}
	if !strings.Contains(check.Note, "超时") {
		t.Fatalf("note %q does not say the check timed out", check.Note)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the probe took %v despite a 50 ms timeout", elapsed)
	}
}

// TestHTTPProbeKeepsA200WhenTheBodyIsHuge pins that a large body is not read:
// the status is the answer, and a body read error after a 200 must not turn a
// working relay into a failure.
func TestHTTPProbeKeepsA200WhenTheBodyIsHuge(t *testing.T) {
	body := strings.Repeat("x", 128<<10)
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	check, _ := prober.ProbeNode(context.Background(), httpOnlyTarget())
	if !check.OK {
		t.Fatalf("HTTP check failed on a 200 with a large body: %+v", check)
	}
}

// TestSTUNProbeAcceptsTheTestServersReply pins the UDP half against a real STUN
// server on loopback: a matching transaction ID is what proves the node is
// reachable over UDP.
func TestSTUNProbeAcceptsTheTestServersReply(t *testing.T) {
	addr, cleanup := stuntest.Serve(t)
	defer cleanup()
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	target := httpOnlyTarget()
	target.HostName = "127.0.0.1"
	target.STUNPort = addr.Port
	_, check := prober.ProbeNode(context.Background(), target)
	if !check.Ran {
		t.Fatal("STUN check did not run")
	}
	if !check.OK {
		t.Fatalf("STUN check failed against a live test server: %+v", check)
	}
}

// TestSTUNProbeReportsSilence pins the timeout: a UDP socket that swallows the
// request (no ICMP, no reply) is what a firewalled STUN port looks like.
func TestSTUNProbeReportsSilence(t *testing.T) {
	sink, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error: %v", err)
	}
	defer sink.Close()
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	prober.Timeout = 50 * time.Millisecond
	target := httpOnlyTarget()
	target.HostName = "127.0.0.1"
	target.STUNPort = sink.LocalAddr().(*net.UDPAddr).Port
	_, check := prober.ProbeNode(context.Background(), target)
	if check.OK {
		t.Fatal("silence was read as a working STUN server")
	}
	if !strings.Contains(check.Note, "超时") {
		t.Fatalf("note %q does not say the check timed out", check.Note)
	}
}

// TestSTUNProbeIgnoresAForeignTransaction pins the transaction check: a stray
// packet from another probe must not be taken as this node's answer.
func TestSTUNProbeIgnoresAForeignTransaction(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error: %v", err)
	}
	defer server.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := server.ReadFrom(buf)
			if err != nil {
				return
			}
			if !stun.Is(buf[:n]) {
				continue
			}
			// Someone else's transaction: the probe must keep waiting.
			_, _ = server.WriteTo(stun.Response(stun.NewTxID(), netip.MustParseAddrPort("127.0.0.1:9")), from)
		}
	}()
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	prober.Timeout = 100 * time.Millisecond
	target := httpOnlyTarget()
	target.HostName = "127.0.0.1"
	target.STUNPort = server.LocalAddr().(*net.UDPAddr).Port
	_, check := prober.ProbeNode(context.Background(), target)
	if check.OK {
		t.Fatal("a foreign transaction ID was accepted")
	}
}

// TestSTUNOnlyNodeIsJudgedBySTUNAlone pins the exception: a node with no relay
// has nothing to answer on HTTPS, so its UDP result is the whole story.
func TestSTUNOnlyNodeIsJudgedBySTUNAlone(t *testing.T) {
	node := RelayNodeProbe{
		STUNOnly: true,
		HTTP:     RelayCheck{Ran: false},
		STUN:     RelayCheck{Ran: true, OK: true},
	}
	if !node.Available() {
		t.Fatal("a STUN-only node that answers STUN is not available")
	}
}

// TestNodeWithoutSTUNPortSkipsTheUDPCheck pins that a node opting out of STUN
// (STUNPort -1) reports an unrun check instead of a failure.
func TestNodeWithoutSTUNPortSkipsTheUDPCheck(t *testing.T) {
	prober := latencyCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, check := prober.ProbeNode(context.Background(), httpOnlyTarget())
	if check.Ran {
		t.Fatalf("the UDP check ran for a node with STUNPort -1: %+v", check)
	}
}

// TestRelayNodeProbeNeedsTheRelayChannel pins the rule that a working STUN
// server does not make a broken relay usable: devices connect through HTTPS.
func TestRelayNodeProbeNeedsTheRelayChannel(t *testing.T) {
	node := RelayNodeProbe{
		HTTP: RelayCheck{Ran: true, OK: false, Note: "超时"},
		STUN: RelayCheck{Ran: true, OK: true},
	}
	if node.Available() {
		t.Fatal("a node whose relay is down was reported available because STUN answered")
	}
}

// TestProbeURLUsesTheNodesAlternateTLSPort pins tailcfg's meaning of DERPPort:
// it is the DERP HTTPS port, not a plaintext one.
func TestProbeURLUsesTheNodesAlternateTLSPort(t *testing.T) {
	got := probeURL(RelayProbeTarget{HostName: "relay.example.com", DERPPort: 8443})
	want := "https://relay.example.com:8443/derp/latency-check"
	if got != want {
		t.Fatalf("probeURL() = %q, want %q", got, want)
	}
}

// TestProbeURLFallsBackToPort443 pins the zero value: a map that leaves
// DERPPort out means the default TLS port.
func TestProbeURLFallsBackToPort443(t *testing.T) {
	got := probeURL(RelayProbeTarget{HostName: "relay.example.com"})
	want := "https://relay.example.com:443/derp/latency-check"
	if got != want {
		t.Fatalf("probeURL() = %q, want %q", got, want)
	}
}

// TestSTUNPortDefaultsTo3478 pins the other zero value: tailcfg says STUNPort 0
// means 3478, and -1 means the node has no STUN at all.
func TestSTUNPortDefaultsTo3478(t *testing.T) {
	got, ok := stunAddress(RelayProbeTarget{HostName: "relay.example.com"})
	if !ok || got != "relay.example.com:3478" {
		t.Fatalf("stunAddress() = (%q, %v), want relay.example.com:3478", got, ok)
	}
	if _, ok := stunAddress(RelayProbeTarget{HostName: "relay.example.com", STUNPort: -1}); ok {
		t.Fatal("stunAddress() offered an address for a node without STUN")
	}
}

// TestProbeTLSConfigVerifiesTheNodesCertName pins domain fronting: the TLS
// handshake names the dialed host, but the certificate must be valid for
// CertName. This mirrors derphttp.Client.tlsClient.
func TestProbeTLSConfigVerifiesTheNodesCertName(t *testing.T) {
	conf := probeTLSConfig(RelayProbeTarget{HostName: "front.example.com", CertName: "relay.example.net"})
	if conf.ServerName != "front.example.com" {
		t.Fatalf("ServerName = %q, want the dialed host", conf.ServerName)
	}
	if conf.VerifyPeerCertificate == nil {
		t.Fatal("no certificate check for the node's CertName")
	}
}

// TestProbeTLSConfigPinsASelfSignedHash pins the self-signed case: a
// sha256-raw: CertName pins the certificate instead of a name.
func TestProbeTLSConfigPinsASelfSignedHash(t *testing.T) {
	pin := "sha256-raw:" + strings.Repeat("ab", 32)
	conf := probeTLSConfig(RelayProbeTarget{HostName: "203.0.113.9", CertName: pin})
	if conf.ServerName != "203.0.113.9" {
		t.Fatalf("ServerName = %q, want the dialed host", conf.ServerName)
	}
	if conf.VerifyConnection == nil {
		t.Fatal("no certificate pin for a sha256-raw CertName")
	}
}

// TestProbeTLSConfigKeepsVerificationOn pins the default: without a CertName
// the system roots decide, so a plain node is never probed insecurely.
func TestProbeTLSConfigKeepsVerificationOn(t *testing.T) {
	conf := probeTLSConfig(RelayProbeTarget{HostName: "relay.example.com"})
	if conf.InsecureSkipVerify || conf.VerifyPeerCertificate != nil || conf.VerifyConnection != nil {
		t.Fatal("the plain node's TLS config skips the ordinary verification")
	}
	if conf.ServerName != "relay.example.com" {
		t.Fatalf("ServerName = %q, want the node's host name", conf.ServerName)
	}
}
