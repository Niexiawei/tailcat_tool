package core

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// probeTestMap is two regions and three nodes: one plain node, one with STUN
// switched off, one STUN-only.
const probeTestMap = `{"Regions":{
 "901":{"RegionID":901,"RegionCode":"a","Nodes":[
   {"Name":"901a","RegionID":901,"HostName":"a1.example.com","DERPPort":443},
   {"Name":"901b","RegionID":901,"HostName":"a2.example.com","DERPPort":443,"STUNPort":-1}]},
 "902":{"RegionID":902,"RegionCode":"b","Nodes":[
   {"Name":"902a","RegionID":902,"HostName":"b1.example.com","DERPPort":443,"STUNOnly":true}]}}}`

// wideProbeMap is four regions of three nodes: more work than eight workers can
// start at once, which is what makes the cycle budget observable.
const wideProbeMap = `{"Regions":{
 "901":{"RegionID":901,"RegionCode":"a","Nodes":[
   {"Name":"1a","RegionID":901,"HostName":"1a.example.com"},{"Name":"1b","RegionID":901,"HostName":"1b.example.com"},{"Name":"1c","RegionID":901,"HostName":"1c.example.com"}]},
 "902":{"RegionID":902,"RegionCode":"b","Nodes":[
   {"Name":"2a","RegionID":902,"HostName":"2a.example.com"},{"Name":"2b","RegionID":902,"HostName":"2b.example.com"},{"Name":"2c","RegionID":902,"HostName":"2c.example.com"}]},
 "903":{"RegionID":903,"RegionCode":"c","Nodes":[
   {"Name":"3a","RegionID":903,"HostName":"3a.example.com"},{"Name":"3b","RegionID":903,"HostName":"3b.example.com"},{"Name":"3c","RegionID":903,"HostName":"3c.example.com"}]},
 "904":{"RegionID":904,"RegionCode":"d","Nodes":[
   {"Name":"4a","RegionID":904,"HostName":"4a.example.com"},{"Name":"4b","RegionID":904,"HostName":"4b.example.com"},{"Name":"4c","RegionID":904,"HostName":"4c.example.com"}]}}}`

// fakeNodeProber answers from a table instead of the network, counts the nodes
// it was asked about, and can hold every probe until the cycle is over.
type fakeNodeProber struct {
	mu   sync.Mutex
	seen []string
	// dead names the nodes whose relay must report a failure.
	dead map[string]bool
	// hold, when set, blocks each probe until it is closed or the cycle ends.
	hold chan struct{}
}

func (f *fakeNodeProber) ProbeNode(ctx context.Context, target RelayProbeTarget) (RelayCheck, RelayCheck) {
	f.mu.Lock()
	f.seen = append(f.seen, target.Name)
	hold := f.hold
	dead := f.dead[target.Name]
	f.mu.Unlock()
	httpCheck := RelayCheck{Ran: !target.STUNOnly, OK: !dead && !target.STUNOnly}
	stunCheck := RelayCheck{Ran: target.STUNPort >= 0, OK: !dead}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			httpCheck.Note = relayProbeNoteTimeout
			stunCheck.Note = relayProbeNoteTimeout
			return httpCheck, stunCheck
		}
	}
	return httpCheck, stunCheck
}

func (f *fakeNodeProber) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// probeFixture is a manager that answers probes from the table, never the
// network. The default node list points at a server answering 500: no test
// touches tailcat's real list, and a held cycle reaches the preset at once
// instead of waiting behind the default source.
func probeFixture(t *testing.T, prober *fakeNodeProber, opts ...func(*ManagerOptions)) *managerFixture {
	t.Helper()
	unavailable, _ := relayTestServer(t, http.StatusInternalServerError, "")
	all := append([]func(*ManagerOptions){
		func(o *ManagerOptions) { o.RelayProber = prober },
		func(o *ManagerOptions) { o.DefaultNodeListURL = unavailable.URL + "/derpmap.json" },
	}, opts...)
	f := newManagerFixture(t, all...)
	// A fixture with an interval starts a loop; stop it before the test's
	// variables go away, or the next test sees a goroutine reading them.
	t.Cleanup(func() { f.m.Shutdown(context.Background()) })
	return f
}

// saveProbePreset stores one preset and returns its canonical source.
func saveProbePreset(t *testing.T, m *Manager, name, source string) string {
	t.Helper()
	if _, err := m.SaveRelayPreset(SaveRelayPresetRequest{Name: name, Source: source}); err != nil {
		t.Fatalf("SaveRelayPreset() error: %v", err)
	}
	for _, preset := range m.RelayPresets() {
		if preset.Name == name {
			return preset.Source
		}
	}
	t.Fatalf("preset %q was not saved", name)
	return ""
}

// awaitProbeCycle waits for the running cycle to finish, so a test can read
// what it wrote.
func awaitProbeCycle(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !m.RelayProbeRunning() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the probe cycle did not finish")
}

func TestProbeCycleCoversEveryNodeOfAMap(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", probeTestMap)

	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)

	probe, ok := f.m.RelayProbe(source)
	if !ok {
		t.Fatal("no result for the preset that was just probed")
	}
	names := make([]string, 0, len(probe.Nodes))
	for _, node := range probe.Nodes {
		names = append(names, node.Name)
	}
	if strings.Join(names, ",") != "901a,901b,902a" {
		t.Fatalf("probed nodes = %v, want every node of the map", names)
	}
	if probe.CheckedAt.IsZero() {
		t.Fatal("CheckedAt was left zero")
	}
	// 901b has no STUN, so it is judged by its relay; 902a is STUN-only.
	if ok, total := probe.Available(); ok != 3 || total != 3 {
		t.Fatalf("Available() = %d/%d, want 3/3", ok, total)
	}
}

func TestRelayProbeAnswersFalseBeforeTheFirstCycle(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", probeTestMap)
	if _, ok := f.m.RelayProbe(source); ok {
		t.Fatal("a result was reported before any cycle ran")
	}
	if calls := prober.calls(); len(calls) != 0 {
		t.Fatalf("the fixture probed %d nodes without being asked", len(calls))
	}
}

func TestProbeCycleParsesAnInlineMapWithoutNetwork(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", probeTestMap)
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	// The map comes back from the cycle: the detail page needs no fetch of its
	// own after a probe.
	if _, ok := f.m.CachedRelayMapPreview(source); !ok {
		t.Fatal("the probed map was not remembered as a preview")
	}
}

func TestProbeCycleDownloadsAURLMapFirst(t *testing.T) {
	server, hits := relayTestServer(t, http.StatusOK, probeTestMap)
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", server.URL+"/map.json")
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("requests to the map = %d, want 1", got)
	}
	probe, ok := f.m.RelayProbe(source)
	if !ok || len(probe.Nodes) != 3 {
		t.Fatalf("probe = %+v, want the three nodes of the downloaded map", probe)
	}
}

func TestProbeCycleReportsAMapThatCannotBeLoaded(t *testing.T) {
	server, _ := relayTestServer(t, http.StatusInternalServerError, probeTestMap)
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", server.URL+"/map.json")
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	probe, ok := f.m.RelayProbe(source)
	if !ok {
		t.Fatal("a failed cycle wrote no result at all")
	}
	if !strings.Contains(probe.Err, "500") {
		t.Fatalf("Err = %q, want the status code in it", probe.Err)
	}
	if len(probe.Nodes) != 0 {
		t.Fatalf("Nodes = %+v, want none for a map that never loaded", probe.Nodes)
	}
	if ok, total := probe.Available(); ok != 0 || total != 0 {
		t.Fatalf("Available() = %d/%d, want no count at all", ok, total)
	}
	if calls := prober.calls(); len(calls) != 0 {
		t.Fatalf("probed %v although the map never loaded", calls)
	}
}

func TestProbeCycleKeepsGoingAfterOneNodeTimesOut(t *testing.T) {
	prober := &fakeNodeProber{dead: map[string]bool{"901a": true}}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", probeTestMap)
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	probe, ok := f.m.RelayProbe(source)
	if !ok {
		t.Fatal("no result")
	}
	if probe.Err != "" {
		t.Fatalf("one dead node failed the whole cycle: %q", probe.Err)
	}
	if ok, total := probe.Available(); ok != 2 || total != 3 {
		t.Fatalf("Available() = %d/%d, want the other two nodes counted", ok, total)
	}
}

func TestProbeCycleCopiesResultsOut(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", probeTestMap)
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	first, _ := f.m.RelayProbe(source)
	first.Nodes[0].Name = "改掉"
	first.Nodes[0].HTTP.OK = false
	second, _ := f.m.RelayProbe(source)
	if second.Nodes[0].Name != "901a" || !second.Nodes[0].HTTP.OK {
		t.Fatal("the caller's copy aliases the manager's result")
	}
}

func TestProbeCycleDoesNotBlockOtherManagerCalls(t *testing.T) {
	prober := &fakeNodeProber{hold: make(chan struct{})}
	f := probeFixture(t, prober)
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	f.m.RefreshRelayProbes()
	defer close(prober.hold)
	done := make(chan struct{})
	go func() {
		_ = f.m.Profiles()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Profiles() blocked while a probe cycle was in flight")
	}
}

func TestProbeCycleNotifiesTheWindow(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	changed := f.m.Changed()
	f.m.RefreshRelayProbes()
	for range 2 {
		select {
		case <-changed:
		case <-time.After(2 * time.Second):
			t.Fatal("the window was not told about the cycle")
		}
	}
	awaitProbeCycle(t, f.m)
}

func TestRefreshWhileRunningDoesNotStartASecondCycle(t *testing.T) {
	prober := &fakeNodeProber{hold: make(chan struct{})}
	f := probeFixture(t, prober)
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	f.m.RefreshRelayProbes()
	// Let the first cycle start every worker before the extra calls.
	deadline := time.Now().Add(2 * time.Second)
	for len(prober.calls()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	f.m.RefreshRelayProbes()
	f.m.RefreshRelayProbes()
	close(prober.hold)
	awaitProbeCycle(t, f.m)
	time.Sleep(20 * time.Millisecond)
	if calls := prober.calls(); len(calls) != 3 {
		t.Fatalf("probed %d nodes, want 3: a second cycle ran on top of the first", len(calls))
	}
}

func TestProbeCycleProbesTheDefaultNodeList(t *testing.T) {
	server, hits := relayTestServer(t, http.StatusOK, probeTestMap)
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober, func(o *ManagerOptions) {
		o.DefaultNodeListURL = server.URL + "/derpmap.json"
	})
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("requests to the default node list = %d, want 1", got)
	}
	probe, ok := f.m.RelayProbe("")
	if !ok || len(probe.Nodes) != 3 {
		t.Fatalf("default node list probe = %+v, want three nodes", probe)
	}
}

func TestNoProbeLoopWithoutAnInterval(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober)
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	time.Sleep(80 * time.Millisecond)
	if calls := prober.calls(); len(calls) != 0 {
		t.Fatalf("probed %v although no interval was set", calls)
	}
}

func TestProbeLoopRepeatsOnItsInterval(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober, func(o *ManagerOptions) { o.RelayProbeInterval = 10 * time.Millisecond })
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	deadline := time.Now().Add(5 * time.Second)
	for len(prober.calls()) < 6 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if calls := prober.calls(); len(calls) < 6 {
		t.Fatalf("probed %d nodes in five seconds, want at least two cycles", len(calls))
	}
}

func TestShutdownStopsTheProbeLoop(t *testing.T) {
	prober := &fakeNodeProber{}
	f := probeFixture(t, prober, func(o *ManagerOptions) { o.RelayProbeInterval = 10 * time.Millisecond })
	saveProbePreset(t, f.m, "公司自建", probeTestMap)
	deadline := time.Now().Add(5 * time.Second)
	for len(prober.calls()) < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	f.m.Shutdown(context.Background())
	settled := len(prober.calls())
	time.Sleep(100 * time.Millisecond)
	if after := len(prober.calls()); after != settled {
		t.Fatalf("probed %d more nodes after shutdown", after-settled)
	}
}

func TestProbeCycleGivesUpAfterItsBudget(t *testing.T) {
	restore := relayProbeBudget
	relayProbeBudget = 30 * time.Millisecond
	t.Cleanup(func() { relayProbeBudget = restore })
	prober := &fakeNodeProber{hold: make(chan struct{})}
	defer close(prober.hold)
	f := probeFixture(t, prober)
	source := saveProbePreset(t, f.m, "公司自建", wideProbeMap)
	f.m.RefreshRelayProbes()
	awaitProbeCycle(t, f.m)
	probe, ok := f.m.RelayProbe(source)
	if !ok {
		t.Fatal("a cycle that ran out of budget wrote no result")
	}
	unmeasured := 0
	for _, node := range probe.Nodes {
		if !node.HTTP.Ran && !node.STUN.Ran {
			unmeasured++
		}
	}
	if unmeasured == 0 {
		t.Fatalf("every one of %d nodes was probed despite a 30 ms budget", len(probe.Nodes))
	}
}
