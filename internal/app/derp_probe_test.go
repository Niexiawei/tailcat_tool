package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// listProbeMap is one region with four plain nodes, so a dead one still leaves
// a count worth showing.
const listProbeMap = `{"Regions":{"901":{"RegionID":901,"RegionCode":"a","Nodes":[
 {"Name":"a1","RegionID":901,"HostName":"a1.example.com"},
 {"Name":"a2","RegionID":901,"HostName":"a2.example.com"},
 {"Name":"a3","RegionID":901,"HostName":"a3.example.com"},
 {"Name":"a4","RegionID":901,"HostName":"a4.example.com"}]}}}`

// defaultProbeMap is the default node list as a test serves it: three nodes,
// so a summary has something to count.
const defaultProbeMap = `{"Regions":{"1":{"RegionID":1,"RegionCode":"d","Nodes":[
 {"Name":"d1","RegionID":1,"HostName":"d1.example.com"},
 {"Name":"d2","RegionID":1,"HostName":"d2.example.com"},
 {"Name":"d3","RegionID":1,"HostName":"d3.example.com"}]}}}`

// listProber answers node probes from a table and can hold them, so a test can
// look at the page while a cycle is still in flight.
type listProber struct {
	mu   sync.Mutex
	dead map[string]bool
	hold chan struct{}
	seen int
}

func (p *listProber) ProbeNode(ctx context.Context, target core.RelayProbeTarget) (core.RelayCheck, core.RelayCheck) {
	p.mu.Lock()
	p.seen++
	hold := p.hold
	dead := p.dead[target.Name]
	p.mu.Unlock()
	httpCheck := core.RelayCheck{Ran: !target.STUNOnly, OK: !dead && !target.STUNOnly}
	stunCheck := core.RelayCheck{Ran: target.STUNPort >= 0, OK: !dead}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			httpCheck.OK = false
			httpCheck.Note = "超时"
			stunCheck.OK = false
			stunCheck.Note = "超时"
			return httpCheck, stunCheck
		}
	}
	return httpCheck, stunCheck
}

func (p *listProber) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

// failingListServer answers 500, so a cycle against it finishes at once without
// reaching tailcat's real list.
func failingListServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/derpmap.json"
}

// probeDerpViewModel is the DERP list with the given presets and a prober that
// never touches the network. tweak, when set, adjusts the manager options
// before it is built (the default node list, usually).
func probeDerpViewModel(t *testing.T, prober core.NodeProber, tweak func(*core.ManagerOptions), presets ...core.SaveRelayPresetRequest) *ViewModel {
	t.Helper()
	creds := core.NewMemoryCredentialStore()
	opts := core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: []core.Profile{{ID: uuid.New(), Name: "E245", RemotePort: 5555}},
			Tools:    core.ToolSettings{ADB: os.Args[0]},
		},
		Credentials:        creds,
		Identities:         core.NewIdentityStore(t.TempDir()),
		Runner:             stubRunner{},
		Locate:             func(string) (string, bool) { return "", false },
		MonitorInterval:    3600 * time.Second,
		RelayProber:        prober,
		DefaultNodeListURL: failingListServer(t),
	}
	if tweak != nil {
		tweak(&opts)
	}
	m := core.NewManager(opts)
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	for _, profile := range m.Profiles() {
		if err := creds.Write(testAddress, profile.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, preset := range presets {
		if _, err := m.SaveRelayPreset(preset); err != nil {
			t.Fatalf("SaveRelayPreset() error: %v", err)
		}
	}
	vm := NewViewModel(m, func(string) (string, bool) { return "", false })
	vm.OpenDerp()
	return vm
}

// waitForProbes waits for the running cycle to land its results.
func waitForProbes(t *testing.T, vm *ViewModel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !vm.Manager.RelayProbeRunning() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the probe cycle did not finish")
}

func TestRelayProbeCellTextNamesTheThreeStates(t *testing.T) {
	four := core.RelayProbe{Nodes: []core.RelayNodeProbe{
		{HTTP: core.RelayCheck{Ran: true, OK: true}},
		{HTTP: core.RelayCheck{Ran: true, OK: true}},
		{HTTP: core.RelayCheck{Ran: true, OK: true}},
		{HTTP: core.RelayCheck{Ran: true, OK: false, Note: "超时"}},
	}}
	if got := relayProbeCell(four); got != "3/4" {
		t.Fatalf("relayProbeCell() = %q, want 3/4", got)
	}
	if got := relayProbeCell(core.RelayProbe{}); got != "未检测" {
		t.Fatalf("relayProbeCell() = %q before a cycle, want 未检测", got)
	}
	if got := relayProbeCell(core.RelayProbe{Err: "下载 DERP 节点配置失败：服务器返回 500"}); got != "检测失败" {
		t.Fatalf("relayProbeCell() = %q for a failed map, want 检测失败", got)
	}
}

func TestDerpListShowsAvailableNodesOverTotal(t *testing.T) {
	vm := probeDerpViewModel(t, &listProber{dead: map[string]bool{"a4": true}}, nil,
		core.SaveRelayPresetRequest{Name: "公司自建", Source: listProbeMap})
	vm.Manager.RefreshRelayProbes()
	waitForProbes(t, vm)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("3/4") {
		t.Fatalf("the list does not show 3/4; texts = %v", tt.Texts())
	}
}

func TestDerpListSaysNotDetectedBeforeTheFirstCycle(t *testing.T) {
	vm := probeDerpViewModel(t, &listProber{}, nil,
		core.SaveRelayPresetRequest{Name: "公司自建", Source: listProbeMap})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("未检测") {
		t.Fatalf("an unprobed preset says nothing about it; texts = %v", tt.Texts())
	}
}

func TestDerpListSaysProbeFailedWhenTheMapCannotBeLoaded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	// The default row succeeds, so 检测失败 appears exactly once: the test
	// hovers that cell, and a second one would come first in reading order.
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(defaultProbeMap))
	}))
	defer working.Close()
	vm := probeDerpViewModel(t, &listProber{}, func(o *core.ManagerOptions) {
		o.DefaultNodeListURL = working.URL + "/derpmap.json"
	}, core.SaveRelayPresetRequest{Name: "公司自建", Source: server.URL + "/map.json"})
	vm.Manager.RefreshRelayProbes()
	waitForProbes(t, vm)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("检测失败") {
		t.Fatalf("a map that never loaded shows no failure; texts = %v", tt.Texts())
	}
	// The reason belongs in a tooltip: the column is 76 DIP wide and the
	// message is a sentence.
	rect, ok := tt.Find("检测失败")
	if !ok {
		t.Fatalf("no 检测失败 cell; texts = %v", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Advance(1500 * time.Millisecond)
	tt.Frame()
	reason := "下载 DERP 节点配置失败：服务器返回 500"
	if _, ok := tt.AccessByRole(ui.RoleTooltip, reason); !ok {
		t.Fatalf("hovering 检测失败 showed no reason; access = %v", tt.AccessAll())
	}
}

func TestDerpProbeNowStartsACycle(t *testing.T) {
	prober := &listProber{}
	vm := probeDerpViewModel(t, prober, nil,
		core.SaveRelayPresetRequest{Name: "公司自建", Source: listProbeMap})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("立即检测"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for prober.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if prober.calls() == 0 {
		t.Fatal("立即检测 started no probe")
	}
	waitForProbes(t, vm)
	tt.Frame()
	if !tt.HasText("4/4") {
		t.Fatalf("the cycle's result never reached the list; texts = %v", tt.Texts())
	}
}

func TestDerpProbeColumnShowsWhenTheCycleIsRunning(t *testing.T) {
	prober := &listProber{hold: make(chan struct{})}
	vm := probeDerpViewModel(t, prober, nil,
		core.SaveRelayPresetRequest{Name: "公司自建", Source: listProbeMap})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("立即检测"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	// The window has to say it is working, or a slow map looks like a dead
	// button.
	deadline := time.Now().Add(2 * time.Second)
	for !tt.HasText("检测中") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !tt.HasText("检测中") {
		t.Fatalf("no 检测中 while a cycle runs; texts = %v", tt.Texts())
	}
	close(prober.hold)
	waitForProbes(t, vm)
	tt.Frame()
	if tt.HasText("检测中") {
		t.Fatalf("检测中 stayed after the cycle ended; texts = %v", tt.Texts())
	}
}

func TestDerpDefaultNodeRowShowsItsProbeSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(defaultProbeMap))
	}))
	defer server.Close()
	prober := &listProber{dead: map[string]bool{"d3": true}}
	vm := probeDerpViewModel(t, prober, func(o *core.ManagerOptions) {
		o.DefaultNodeListURL = server.URL + "/derpmap.json"
	})
	vm.Manager.RefreshRelayProbes()
	waitForProbes(t, vm)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if !tt.HasText("可用 2/3") {
		t.Fatalf("the default node row shows no probe summary; texts = %v", tt.Texts())
	}
}

func TestDerpLastProbeTimeIsShownOnceACycleRan(t *testing.T) {
	vm := probeDerpViewModel(t, &listProber{}, nil,
		core.SaveRelayPresetRequest{Name: "公司自建", Source: listProbeMap})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if tt.HasText("上次检测") {
		t.Fatalf("a time is shown before any cycle; texts = %v", tt.Texts())
	}
	vm.Manager.RefreshRelayProbes()
	waitForProbes(t, vm)
	tt.Frame()
	if !tt.HasText("上次检测") {
		t.Fatalf("no 上次检测 after a cycle; texts = %v", tt.Texts())
	}
}
