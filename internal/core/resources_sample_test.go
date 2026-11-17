package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// sampleProc is one process in a fake sampling round.
type sampleProc struct {
	pid                 int
	utime, stime, start uint64
	rssKB               uint64
	cmd                 string
}

// sampleRound is one fake device reading; its String() is exactly the script
// output the real device prints.
type sampleRound struct {
	stat               string // the aggregate /proc/stat line's numbers
	memTotal, memAvail uint64 // kB
	cores              int
	model              string
	gpu                string
	procs              []sampleProc
}

func (r sampleRound) String() string {
	var b strings.Builder
	b.WriteString("@@STAT\ncpu " + r.stat + "\n")
	fmt.Fprintf(&b, "@@MEM\nMemTotal: %d kB\nMemAvailable: %d kB\n", r.memTotal, r.memAvail)
	b.WriteString("@@GPU\n" + r.gpu + "\n")
	b.WriteString("@@PROC\n")
	for _, p := range r.procs {
		fmt.Fprintf(&b, "@@P %d|%d|%d (comm) S 1 %d %d 0 -1 0 0 0 0 0 %d %d 0 0 20 0 1 0 %d 0 0|%s\n",
			p.pid, p.rssKB, p.pid, p.pid, p.pid, p.utime, p.stime, p.start, p.cmd)
	}
	return b.String()
}

// hardwareOutput is what hardwareScript prints: the identity that never
// changes while a device is connected.
func (r sampleRound) hardwareOutput() string {
	return fmt.Sprintf("@@CPU\n%d\n%s\n@@GPU\n", r.cores, r.model)
}

// defaultRound is one healthy device reading with two applications.
func defaultRound() sampleRound {
	return sampleRound{
		stat:     "100 0 100 800 0 0 0 0 0 0",
		memTotal: 8000000, memAvail: 2000000,
		cores: 8, model: "Hardware\t: Qualcomm Technologies, Inc SM8150",
		procs: []sampleProc{
			{pid: 100, utime: 10, stime: 0, start: 500, rssKB: 20480, cmd: "com.foo"},
			{pid: 101, utime: 5, stime: 0, start: 600, rssKB: 1024, cmd: "com.foo:remote"},
			{pid: 200, utime: 1, stime: 0, start: 700, rssKB: 4096, cmd: "system_server"},
		},
	}
}

// serveRounds makes the fixture answer each sampling call with the next round,
// repeating the last one when the test asks for more calls than it prepared.
func (f *managerFixture) serveRounds(t *testing.T, rounds ...sampleRound) {
	t.Helper()
	var mu sync.Mutex
	next := 0
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if containsSubstring(c.Args, "@@CPU") {
			round := rounds[min(next, len(rounds)-1)]
			return Result{Output: round.hardwareOutput()}, nil
		}
		if !containsSubstring(c.Args, "@@STAT") {
			return Result{}, nil
		}
		mu.Lock()
		defer mu.Unlock()
		round := rounds[min(next, len(rounds)-1)]
		next++
		return Result{Output: round.String()}, nil
	})
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

func TestSampleResourcesNeedsTwoReadingsForCPU(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)

	first, second := defaultRound(), defaultRound()
	first.stat = "100 0 100 800 0 0 0 0 0 0"
	second.stat = "200 0 200 1600 0 0 0 0 0 0"
	second.procs[0].utime = 60
	second.procs[0].stime = 40
	f.serveRounds(t, first, second)

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	if snap.CPU.Available {
		t.Fatalf("the first reading claims a CPU percentage: %+v", snap.CPU)
	}

	snap, err = f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	if !snap.CPU.Available {
		t.Fatal("the second reading still has no CPU percentage")
	}
	if diff := snap.CPU.Percent - 20; diff > 0.01 || diff < -0.01 {
		t.Fatalf("CPU = %v, want 20", snap.CPU.Percent)
	}
	if got := snap.Apps[0].Package; got != "com.foo" {
		t.Fatalf("first app = %q", got)
	}
	if diff := snap.Apps[0].CPUPercent - 9; diff > 0.01 || diff < -0.01 {
		t.Fatalf("com.foo CPU = %v, want 9", snap.Apps[0].CPUPercent)
	}
}

func TestSampleResourcesReadsMemoryAndCPUModel(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.serveRounds(t, defaultRound())

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	if !snap.Memory.OK {
		t.Fatalf("memory is unavailable: %+v", snap.Memory)
	}
	if snap.Memory.Total != 8000000*1024 || snap.Memory.Used != 6000000*1024 {
		t.Fatalf("memory = %+v", snap.Memory)
	}
	if diff := snap.Memory.Percent - 75; diff > 0.01 || diff < -0.01 {
		t.Fatalf("memory percent = %v, want 75", snap.Memory.Percent)
	}
	if snap.CPU.Cores != 8 {
		t.Fatalf("Cores = %d, want 8", snap.CPU.Cores)
	}
	if snap.CPU.Model != "Qualcomm Technologies, Inc SM8150" {
		t.Fatalf("Model = %q", snap.CPU.Model)
	}
}

func TestSampleResourcesReportsGPUUnavailable(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.serveRounds(t, defaultRound())

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	if snap.GPU.Available {
		t.Fatalf("a device without GPU counters reported %+v", snap.GPU)
	}
	if !containsString(snap.Warnings, "该设备不提供 GPU 使用率") {
		t.Fatalf("warnings = %v", snap.Warnings)
	}
}

func TestSampleResourcesReadsGPUBusy(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	round := defaultRound()
	round.gpu = "37 100"
	f.serveRounds(t, round)

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	if !snap.GPU.Available {
		t.Fatalf("GPU counters were ignored: %+v", snap.GPU)
	}
	if diff := snap.GPU.Percent - 37; diff > 0.01 || diff < -0.01 {
		t.Fatalf("GPU percent = %v, want 37", snap.GPU.Percent)
	}
}

func TestSampleResourcesKeepsPreviousSnapshotOnTimeout(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	round := defaultRound()
	var once sync.Once
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if containsSubstring(c.Args, "@@STAT") {
			timedOut := false
			once.Do(func() {})
			if timedOut {
				return Result{TimedOut: true}, nil
			}
			return Result{Output: round.String()}, nil
		}
		return Result{}, nil
	})
	if _, err := f.m.SampleResources(context.Background(), f.id); err != nil {
		t.Fatalf("first SampleResources() error: %v", err)
	}

	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return Result{TimedOut: true}, nil
	})
	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() after a timeout: %v", err)
	}
	if !containsString(snap.Warnings, "采样超时") {
		t.Fatalf("warnings = %v", snap.Warnings)
	}
	if len(snap.Apps) == 0 {
		t.Fatal("the timeout threw away the previous snapshot")
	}
}

func TestSampleResourcesRejectsStaleGeneration(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if containsSubstring(c.Args, "@@STAT") {
			once.Do(func() { close(started) })
			<-release
			return Result{Output: defaultRound().String()}, nil
		}
		return Result{}, nil
	})

	var got error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, got = f.m.SampleResources(context.Background(), f.id)
	}()
	<-started
	f.m.Disconnect(context.Background(), f.id)
	close(release)
	wg.Wait()

	if got != ErrCancelled {
		t.Fatalf("SampleResources() error = %v, want ErrCancelled", got)
	}
	if snap, ok := f.m.Resources(f.id); ok && len(snap.Apps) > 0 {
		t.Fatalf("a disconnected device kept a snapshot: %+v", snap)
	}
}

// fakeAppMeta answers from a fixed map and records what it was asked to fetch.
type fakeAppMeta struct {
	mu        sync.Mutex
	metas     map[string]AppMeta
	requested []string
	refreshed []string
	// statuses is what Status answers; anything missing is "never asked".
	statuses map[string]AppMetaStatus
	// devices records every device key Status and Forget were called with.
	statusDevices []string
	forgotten     []string
}

func (f *fakeAppMeta) Lookup(device, pkg string) (AppMeta, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	meta, ok := f.metas[pkg]
	return meta, ok
}

func (f *fakeAppMeta) Request(_, _, _ string, pkgs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, pkgs...)
}

func (f *fakeAppMeta) Refresh(_, _, _ string, pkgs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, pkgs...)
}

func (f *fakeAppMeta) Status(device, pkg string) (AppMetaStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusDevices = append(f.statusDevices, device)
	status, ok := f.statuses[pkg]
	return status, ok
}

func (f *fakeAppMeta) Forget(device string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, device)
}

// statusKeys is the device keys Status was called with, deduplicated.
func (f *fakeAppMeta) statusKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	keys := make([]string, 0, len(f.statusDevices))
	for _, device := range f.statusDevices {
		if seen[device] {
			continue
		}
		seen[device] = true
		keys = append(keys, device)
	}
	return keys
}

func (f *fakeAppMeta) forgottenKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forgotten...)
}

func TestSampleResourcesUsesAppMetaLabels(t *testing.T) {
	meta := &fakeAppMeta{metas: map[string]AppMeta{
		"com.foo": {Package: "com.foo", Label: "微信", Icon: []byte{1, 2, 3}},
	}}
	f := newManagerFixture(t, func(o *ManagerOptions) { o.AppMeta = meta })
	f.connect(t)
	f.serveRounds(t, defaultRound())

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	var foo AppUsage
	for _, app := range snap.Apps {
		if app.Package == "com.foo" {
			foo = app
		}
	}
	if foo.Label != "微信" || !foo.HasIcon {
		t.Fatalf("com.foo = %+v", foo)
	}
	if len(meta.requested) != 0 || len(meta.refreshed) != 0 {
		t.Fatalf("sampling fetched metadata on its own: requested=%v refreshed=%v", meta.requested, meta.refreshed)
	}
}

func TestSampleResourcesKeepsStaleLabelWithoutFetching(t *testing.T) {
	meta := &fakeAppMeta{metas: map[string]AppMeta{
		"com.foo": {Package: "com.foo", Label: "旧名字", Stale: true},
	}}
	f := newManagerFixture(t, func(o *ManagerOptions) { o.AppMeta = meta })
	f.connect(t)
	f.serveRounds(t, defaultRound())

	snap, err := f.m.SampleResources(context.Background(), f.id)
	if err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	var foo AppUsage
	for _, app := range snap.Apps {
		if app.Package == "com.foo" {
			foo = app
		}
	}
	if foo.Label != "旧名字" {
		t.Fatalf("a stale label was dropped: %+v", foo)
	}
	if !foo.MetaStale {
		t.Fatalf("the snapshot does not mark the stale label: %+v", foo)
	}
	if len(meta.refreshed) != 0 {
		t.Fatalf("a stale entry was refetched without being asked: %v", meta.refreshed)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// deviceKey is stable across reconnects, which is what the metadata cache keys
// its entries by.
func TestDeviceKeyIsTheDeviceID(t *testing.T) {
	id := uuid.New()
	if deviceKey(id) != id.String() {
		t.Fatalf("deviceKey(%v) = %q", id, deviceKey(id))
	}
}
