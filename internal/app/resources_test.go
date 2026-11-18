package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// samplingRound is one fake device reading, in the same sectioned format the
// real sampling script prints.
func samplingRound(stat string, utime uint64, gpu string) string {
	var b strings.Builder
	b.WriteString("@@STAT\ncpu " + stat + "\n")
	b.WriteString("@@MEM\nMemTotal: 8000000 kB\nMemAvailable: 2000000 kB\n")
	b.WriteString("@@GPU\n" + gpu + "\n")
	b.WriteString("@@PROC\n")
	b.WriteString("@@P 100|20480|100 (comm) S 1 100 100 0 -1 0 0 0 0 0 " + itoa(int(utime)) + " 0 0 0 20 0 1 0 500 0 0|com.foo\n")
	b.WriteString("@@P 101|1024|101 (comm) S 1 101 101 0 -1 0 0 0 0 0 5 0 0 0 20 0 1 0 600 0 0|com.foo:remote\n")
	b.WriteString("@@P 200|4096|200 (comm) S 1 200 200 0 -1 0 0 0 0 0 1 0 0 0 20 0 1 0 700 0 0|system_server\n")
	return b.String()
}

// resourceFixture connects a device, runs two sampling rounds and leaves the
// resource tab selected, so the page has numbers to draw.
func resourceFixture(t *testing.T) (*ViewModel, func() []string) {
	t.Helper()
	return resourceFixtureWith(t)
}

func resourceFixtureWith(t *testing.T, opts ...func(*core.ManagerOptions)) (*ViewModel, func() []string) {
	t.Helper()
	round := 0
	vm, calls := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		joined := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(joined, "get-state"):
			return core.Result{Output: "device\n"}, nil
		case strings.Contains(joined, "@@CPU"):
			return core.Result{Output: "@@CPU\n8\nHardware\t: Qualcomm Technologies, Inc SM8150\n@@GPU\n"}, nil
		case strings.Contains(joined, "@@STAT"):
			round++
			if round == 1 {
				return core.Result{Output: samplingRound("100 0 100 800 0 0 0 0 0 0", 10, "")}, nil
			}
			return core.Result{Output: samplingRound("200 0 200 1600 0 0 0 0 0 0", 40, "")}, nil
		}
		return core.Result{}, nil
	}, opts...)
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })

	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := vm.Manager.SampleResources(context.Background(), profile.ID); err != nil {
			t.Fatalf("SampleResources() error: %v", err)
		}
	}
	vm.DetailTab = detailResources
	vm.OpenDevice(profile.ID)
	return vm, calls
}

func TestResourcePageShowsHardwareAndDonutLabels(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 800)

	for _, want := range []string{"资源", "内存", "8 核", "20%", "75%"} {
		if !tt.HasText(want) {
			t.Fatalf("the resource page has no %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestResourcePageSaysGPUUnavailable(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 800)

	if !tt.HasText("不可用") {
		t.Fatalf("a device without GPU counters shows no 不可用; texts = %v", tt.Texts())
	}
	if !tt.HasText("该设备不提供 GPU 使用率") {
		t.Fatalf("the page does not explain the missing GPU counter; texts = %v", tt.Texts())
	}
}

func TestResourcePageNeedsConnectionWhenOffline(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.DetailTab = detailResources
	vm.OpenDevice(profile.ID)

	tt := ui.NewTester(vm.ContentView, 900, 800)
	if !tt.HasText("设备未连接") {
		t.Fatalf("an offline device has no explanation; texts = %v", tt.Texts())
	}
	if tt.HasText("按应用分组") {
		t.Fatalf("an offline device still rendered the process table; texts = %v", tt.Texts())
	}
}

// The resource tab must not keep polling once the page is gone.
func TestResourcePageStopsPollingWhenTheTabCloses(t *testing.T) {
	vm, _ := resourceFixture(t)
	ui.NewTester(vm.ContentView, 900, 800)
	profile := vm.Manager.Profiles()[0]
	if _, ok := vm.Manager.Resources(profile.ID); !ok {
		t.Fatal("the open tab has no reading")
	}

	vm.DetailTab = detailLog
	ui.NewTester(vm.ContentView, 900, 800)
	if _, ok := vm.Manager.Resources(profile.ID); !ok {
		t.Fatal("leaving the tab threw the reading away and would reload the device")
	}
}

// stubAppMeta is the metadata cache as the view sees it.
type stubAppMeta struct {
	mu        sync.Mutex
	metas     map[string]core.AppMeta
	statuses  map[string]core.AppMetaStatus
	requested []string
	refreshed []string
}

func (s *stubAppMeta) Lookup(_, pkg string) (core.AppMeta, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.metas[pkg]
	return meta, ok
}

func (s *stubAppMeta) Request(_, _, _ string, pkgs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requested = append(s.requested, pkgs...)
}

func (s *stubAppMeta) Refresh(_, _, _ string, pkgs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed = append(s.refreshed, pkgs...)
}

func (s *stubAppMeta) Status(_, pkg string) (core.AppMetaStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.statuses[pkg]
	return status, ok
}

func (s *stubAppMeta) Forget(string) {}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// setStatus publishes what the store would answer for one package.
func (s *stubAppMeta) setStatus(pkg string, status core.AppMetaStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statuses == nil {
		s.statuses = map[string]core.AppMetaStatus{}
	}
	s.statuses[pkg] = status
}

func (s *stubAppMeta) refreshedPackages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.refreshed...)
}

// resourceFixtureWithMeta runs the usual fixture against a metadata cache.
func resourceFixtureWithMeta(t *testing.T, metas map[string]core.AppMeta) (*ViewModel, *stubAppMeta) {
	t.Helper()
	meta := &stubAppMeta{metas: metas}
	vm, _ := resourceFixtureWith(t, func(o *core.ManagerOptions) { o.AppMeta = meta })
	return vm, meta
}

func TestProcessTableGroupsByApp(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)

	for _, want := range []string{"按应用分组", "com.foo", "system_server", "2 个进程"} {
		if !tt.HasText(want) {
			t.Fatalf("the process table has no %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestProcessTableShowsAppNameNotPackage(t *testing.T) {
	vm, _ := resourceFixtureWithMeta(t, map[string]core.AppMeta{
		"com.foo": {Package: "com.foo", Label: "微信", Icon: testIconPNG(t)},
	})
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if !tt.HasText("微信") {
		t.Fatalf("the group row does not show the application name; texts = %v", tt.Texts())
	}
}

func TestProcessTableFallsBackToPackageWithoutMeta(t *testing.T) {
	vm, _ := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if !tt.HasText("com.foo") {
		t.Fatalf("a package without metadata disappeared from the table; texts = %v", tt.Texts())
	}
}

func TestProcessTableMarksStaleMeta(t *testing.T) {
	vm, _ := resourceFixtureWithMeta(t, map[string]core.AppMeta{
		"com.foo": {Package: "com.foo", Label: "微信", Stale: true},
	})
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if !tt.HasText("微信") {
		t.Fatalf("a stale name was dropped; texts = %v", tt.Texts())
	}
	if !tt.HasText("7 天前更新") {
		t.Fatalf("a stale name is not marked; texts = %v", tt.Texts())
	}
}

// TestProcessGroupExpandsFromItsChevron pins the row's own affordance: the
// group opens and closes from the arrow at the start of its row, which leaves
// the table's keyboard navigation to the table itself.
func TestProcessGroupExpandsFromItsChevron(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("PID 100") {
		t.Fatal("the process rows are visible before expanding the group")
	}
	if err := tt.Click("展开 com.foo"); err != nil {
		t.Fatalf("the group row has no chevron: %v", err)
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("PID 100") || !tt.HasText("com.foo:remote") {
		t.Fatalf("the expanded group does not show its processes; texts = %v", tt.Texts())
	}
	if err := tt.Click("收起 com.foo"); err != nil {
		t.Fatalf("the expanded group offers no way back: %v", err)
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("PID 100") {
		t.Fatal("the group did not collapse")
	}
}

// TestResourcesPageUsesNoOnevraTable keeps this page on the one table card:
// the onevra table's header does not scroll with its columns.
func TestResourcesPageUsesNoOnevraTable(t *testing.T) {
	assertNoOnevraTable(t, "resources.go")
}

func TestProcessTableSaysGPUUnavailable(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if !tt.HasText("不可用") {
		t.Fatalf("the GPU column does not say 不可用; texts = %v", tt.Texts())
	}
}

func TestResourceToolbarRefreshAsksManagerToRefresh(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	tt := ui.NewTester(vm.ContentView, 900, 900)

	if err := tt.Click("刷新应用信息"); err != nil {
		t.Fatalf("clicking the refresh button: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(meta.refreshedPackages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := meta.refreshedPackages(); len(got) == 0 {
		t.Fatal("the refresh button queued nothing")
	}
	profile := vm.Manager.Profiles()[0]
	if len(vm.resourceStateFor(profile.ID).batch) == 0 {
		t.Fatal("the page does not show that a refresh is running")
	}
}

// settledStatus is a terminal status reached after a batch started, which is
// what tells the toolbar that this package is done.
func settledStatus() core.AppMetaStatus {
	return core.AppMetaStatus{State: core.AppMetaReady, At: time.Now().Add(time.Minute)}
}

func TestRefreshSaysHowManyAreDone(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if err := tt.Click("刷新应用信息"); err != nil {
		t.Fatalf("clicking the refresh button: %v", err)
	}

	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("正在读取应用信息 0/2") {
		t.Fatalf("the toolbar does not say how far the refresh got; texts = %v", tt.Texts())
	}

	meta.setStatus("com.foo", settledStatus())
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("正在读取应用信息 1/2") {
		t.Fatalf("one settled application did not move the count; texts = %v", tt.Texts())
	}
}

func TestRefreshProgressEndsWhenEveryRowIsSettled(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if err := tt.Click("刷新应用信息"); err != nil {
		t.Fatalf("clicking the refresh button: %v", err)
	}

	meta.setStatus("com.foo", settledStatus())
	meta.setStatus("system_server", core.AppMetaStatus{
		State:  core.AppMetaFailed,
		Reason: core.ReasonNoAppPackage,
		Final:  true,
		At:     time.Now().Add(time.Minute),
	})
	tt = ui.NewTester(vm.ContentView, 900, 900)

	if tt.HasText("正在读取应用信息 1/2") || tt.HasText("正在读取应用信息 2/2") {
		t.Fatalf("a finished refresh still reports progress; texts = %v", tt.Texts())
	}
}

func TestRefreshCountsOnlyThePackagesItWillRead(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	meta.setStatus("system_server", core.AppMetaStatus{
		State:  core.AppMetaFailed,
		Reason: core.ReasonNoAppPackage,
		Final:  true,
	})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if err := tt.Click("刷新应用信息"); err != nil {
		t.Fatalf("clicking the refresh button: %v", err)
	}

	// Only com.foo will be read, so the progress must not wait for the process
	// that has no APK to settle.
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("正在读取应用信息 0/1") {
		t.Fatalf("the progress counts rows that will never be read; texts = %v", tt.Texts())
	}

	meta.setStatus("com.foo", settledStatus())
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("正在读取应用信息") {
		t.Fatalf("the refresh never finished; texts = %v", tt.Texts())
	}
}

func TestRefreshDoesNotAskAboutProcessesWithNoAppPackage(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	meta.setStatus("system_server", core.AppMetaStatus{
		State:  core.AppMetaFailed,
		Reason: core.ReasonNoAppPackage,
		Final:  true,
	})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if err := tt.Click("刷新应用信息"); err != nil {
		t.Fatalf("clicking the refresh button: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(meta.refreshedPackages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	refreshed := meta.refreshedPackages()
	if containsString(refreshed, "system_server") {
		t.Fatalf("refreshed = %v; a process with no APK was asked again", refreshed)
	}
	if !containsString(refreshed, "com.foo") {
		t.Fatalf("refreshed = %v; the real application was skipped too", refreshed)
	}
}

func TestResourceToolbarRefreshNeedsAConnection(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.DetailTab = detailResources
	vm.OpenDevice(profile.ID)

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if err := tt.Click("刷新应用信息"); err == nil {
		t.Fatal("an offline page offered the refresh button")
	}
}

func TestResourceViewSwitchesBetweenAppsAndProcesses(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	// The application view groups: one row per application, processes hidden
	// until the group is opened.
	if !tt.HasText("2 个进程") {
		t.Fatalf("the application view does not group; texts = %v", tt.Texts())
	}
	if tt.HasText("PID 100") {
		t.Fatal("the application view lists processes before the group is opened")
	}

	if err := tt.Click("进程"); err != nil {
		t.Fatalf("no 进程 segment; texts = %v", tt.Texts())
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)

	if tt.HasText("2 个进程") {
		t.Fatalf("the process view still groups by application; texts = %v", tt.Texts())
	}
	for _, want := range []string{"PID 100", "PID 101", "PID 200"} {
		if !tt.HasText(want) {
			t.Fatalf("the process view is missing %q; texts = %v", want, tt.Texts())
		}
	}

	if err := tt.Click("应用"); err != nil {
		t.Fatalf("no 应用 segment; texts = %v", tt.Texts())
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("2 个进程") {
		t.Fatalf("switching back did not restore the grouped view; texts = %v", tt.Texts())
	}
}

func TestProcessViewListsEveryProcessOnce(t *testing.T) {
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	vm.resourceStateFor(profile.ID).view = resourceViewProcesses

	tt := ui.NewTester(vm.ContentView, 900, 900)
	count := func(want string) int {
		n := 0
		for _, text := range tt.Texts() {
			if text == want {
				n++
			}
		}
		return n
	}
	for _, pid := range []string{"PID 100", "PID 101", "PID 200"} {
		if got := count(pid); got != 1 {
			t.Fatalf("%q appears %d times in the process view, want once; texts = %v", pid, got, tt.Texts())
		}
	}
	// The application each process belongs to is still readable.
	if !tt.HasText("com.foo") {
		t.Fatalf("the process view does not say which application a process belongs to; texts = %v", tt.Texts())
	}
}

func TestResourceProcessViewHasItsOwnTable(t *testing.T) {
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if _, ok := tt.Find("按应用分组的进程表"); !ok {
		t.Fatalf("the application view has no grouped table; texts = %v", tt.Texts())
	}

	vm.resourceStateFor(profile.ID).view = resourceViewProcesses
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if _, ok := tt.Find("按进程列出的进程表"); !ok {
		t.Fatalf("the process view has no table of its own; texts = %v", tt.Texts())
	}
	if _, ok := tt.Find("按应用分组的进程表"); ok {
		t.Fatal("the process view reused the application table, so its scroll and columns would carry over")
	}
}

func TestAppIconFollowsTheMetadataFetchTime(t *testing.T) {
	first := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	icon := testIconPNG(t)
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{
		"com.foo": {Package: "com.foo", Label: "微信", Icon: icon, FetchedAt: first},
	})
	profile := vm.Manager.Profiles()[0]
	if got := vm.appIcon(profile.ID, "com.foo"); got == nil {
		t.Fatal("the cached icon did not decode")
	}

	// A refresh lands with a newer reading; the page must pick up the new icon
	// instead of the one it decoded before.
	other := testIconPNG(t)
	meta.mu.Lock()
	meta.metas["com.foo"] = core.AppMeta{Package: "com.foo", Label: "微信", Icon: other, FetchedAt: first.Add(time.Hour)}
	meta.mu.Unlock()

	if got := vm.appIcon(profile.ID, "com.foo"); got == nil {
		t.Fatal("the refreshed icon did not decode")
	}
	key := profile.ID.String() + "|com.foo"
	if cached := vm.appIcons[key]; !cached.fetchedAt.Equal(first.Add(time.Hour)) {
		t.Fatalf("the icon cache kept the old reading: %v", cached.fetchedAt)
	}
}

// testIconPNG is a 1×1 PNG the view can decode, as a real APK icon would be.
func testIconPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encoding the test icon: %v", err)
	}
	return buf.Bytes()
}

func TestDemoRunnerAnswersResourceSampling(t *testing.T) {
	res, err := demoRunner{}.Run(context.Background(), core.Command{
		Path: "adb", Args: []string{"-s", "emulator-5554", "shell", "echo @@STAT; echo @@MEM"},
	}, nil)
	if err != nil {
		t.Fatalf("demoRunner.Run() error: %v", err)
	}
	for _, want := range []string{"@@STAT", "@@P ", "@@MEM"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("the demo sampling output has no %q", want)
		}
	}
}

func TestDemoEnvironmentHasAppNamesAndIcons(t *testing.T) {
	env := NewDemoEnvironment()
	profile := env.Manager.Profiles()[0]

	meta, ok := env.Manager.AppMeta(profile.ID, "com.demo.camera")
	if !ok || meta.Label != "相机" {
		t.Fatalf("demo metadata = %+v, ok=%v", meta, ok)
	}
	if len(meta.Icon) == 0 {
		t.Fatal("the demo application has no icon")
	}
	if _, ok := env.Manager.AppMeta(profile.ID, "com.unknown.app"); ok {
		t.Fatal("the demo invents metadata for unknown packages")
	}
}

// samplingRoundMany is one reading with n applications, one process each, for
// layout tests that need a table taller than the window.
func samplingRoundMany(apps int) string {
	var b strings.Builder
	b.WriteString("@@STAT\ncpu 100 0 100 800 0 0 0 0 0 0\n")
	b.WriteString("@@MEM\nMemTotal: 8000000 kB\nMemAvailable: 2000000 kB\n")
	b.WriteString("@@GPU\n")
	b.WriteString("@@PROC\n")
	for i := 0; i < apps; i++ {
		pid := 1000 + i
		b.WriteString("@@P " + itoa(pid) + "|1024|" + itoa(pid) + " (comm) S 1 1 1 0 -1 0 0 0 0 0 1 0 0 0 20 0 1 0 500 0 0|com.app" + itoa(i) + "\n")
	}
	return b.String()
}

// manyAppsViewModel renders the resource tab over a device with n applications.
func manyAppsViewModel(t *testing.T, apps int) *ViewModel {
	t.Helper()
	vm, _ := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		joined := strings.Join(c.Args, " ")
		if strings.Contains(joined, "get-state") {
			return core.Result{Output: "device\n"}, nil
		}
		if strings.Contains(joined, "@@CPU") {
			return core.Result{Output: "@@CPU\n8\nHardware\t: CPU\n@@GPU\n"}, nil
		}
		if strings.Contains(joined, "@@STAT") {
			return core.Result{Output: samplingRoundMany(apps)}, nil
		}
		return core.Result{}, nil
	})
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if _, err := vm.Manager.SampleResources(context.Background(), profile.ID); err != nil {
		t.Fatalf("SampleResources() error: %v", err)
	}
	vm.DetailTab = detailResources
	vm.OpenDevice(profile.ID)
	return vm
}

func TestUsageRingCenterShowsTheUsedShare(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 800)

	// Hover the CPU ring and leave it: the chart's own selection then reports
	// no slice, and the middle would show the slices' total (100%) instead of
	// the usage.
	ring, ok := tt.Find("CPU 占用")
	if !ok {
		t.Fatalf("no CPU ring; texts = %v", tt.Texts())
	}
	tt.Move(ring.X+ring.W/2, ring.Y+90)
	tt.Move(5, 5)

	if tt.HasText("100%") {
		t.Fatalf("the ring centre shows the slices' total; texts = %v", tt.Texts())
	}
	if !tt.HasText("20%") {
		t.Fatalf("the ring centre lost the used share; texts = %v", tt.Texts())
	}
}

func TestHardwareRowSharesTheWidthEqually(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 800)

	var xs []float32
	for _, label := range []string{"CPU", "GPU", "内存"} {
		rect, ok := tt.Find(label)
		if !ok {
			t.Fatalf("the hardware row has no %q; texts = %v", label, tt.Texts())
		}
		xs = append(xs, rect.X)
	}
	first, second := xs[1]-xs[0], xs[2]-xs[1]
	if first-second > 2 || second-first > 2 {
		t.Fatalf("hardware columns are not equal thirds: x = %v", xs)
	}
	if !tt.HasText("5.7 GB / 7.6 GB") {
		t.Fatalf("the memory column shows no total; texts = %v", tt.Texts())
	}
}

func TestResourceTableEndsAboveThePageEdge(t *testing.T) {
	vm := manyAppsViewModel(t, 40)
	const window = 700
	tt := ui.NewTester(vm.ContentView, 900, window)

	rect, ok := tt.Find("按应用分组的进程表")
	if !ok {
		t.Fatalf("no process table; texts = %v", tt.Texts())
	}
	// The device page has 20 DIPs of bottom padding; a scroll area that ends at
	// the window edge is cut by the window frame.
	if bottom := rect.Y + rect.H; bottom > window-20 {
		t.Fatalf("the process table ends at %v, past the page's bottom padding", bottom)
	}
}

func TestProcessTableResolvesOneAppOnDemand(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	tt := ui.NewTester(vm.ContentView, 900, 900)

	// Every row that has no name yet offers to read just that application.
	if err := tt.Click("解析应用信息"); err != nil {
		t.Fatalf("no per-row resolve button; texts = %v", tt.Texts())
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(meta.refreshedPackages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	got := meta.refreshedPackages()
	if len(got) != 1 {
		t.Fatalf("the row button queued %v, want exactly one package", got)
	}
}

// TestProcessTableOffersRefreshOnNamedRows pins the way out of an application
// that renamed or re-iconed itself on the device: the row keeps an action after
// it has a name, and that action reads the app again, cache lifetime or not.
func TestProcessTableOffersRefreshOnNamedRows(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{
		"com.foo": {Package: "com.foo", Label: "微信"},
	})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("重新读取应用信息") {
		t.Fatalf("a named application offers no way to refresh a name the device changed; texts = %v", tt.Texts())
	}
	if err := tt.Click("重新读取应用信息"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(meta.refreshedPackages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	got := meta.refreshedPackages()
	if len(got) != 1 || got[0] != "com.foo" {
		t.Fatalf("the row refresh queued %v, want just com.foo", got)
	}
}

func TestRowShowsSpinnerWhileTheReadIsPending(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	meta.setStatus("com.foo", core.AppMetaStatus{State: core.AppMetaPending})

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if _, ok := tt.Find("正在读取应用信息"); !ok {
		t.Fatalf("a pending read shows no spinner; texts = %v", tt.Texts())
	}
}

func TestResolvingSpinnerEndsWhenTheReadFails(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	meta.setStatus("com.foo", core.AppMetaStatus{State: core.AppMetaPending})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if _, ok := tt.Find("正在读取应用信息"); !ok {
		t.Fatalf("no spinner to start with; texts = %v", tt.Texts())
	}

	// The device has no APK for this process, so the read ends without a name.
	// The row must say so instead of waiting out a two-minute timer.
	meta.setStatus("com.foo", core.AppMetaStatus{
		State:  core.AppMetaFailed,
		Reason: core.ReasonNoAppPackage,
		Final:  true,
	})
	tt = ui.NewTester(vm.ContentView, 900, 900)

	if _, ok := tt.Find("正在读取应用信息"); ok {
		t.Fatalf("the row still spins after the read failed; texts = %v", tt.Texts())
	}
	if !tt.HasText(core.LabelNoAppPackage) {
		t.Fatalf("the failed row does not say why; texts = %v", tt.Texts())
	}
	if tt.HasText("重试读取应用信息") {
		t.Fatal("a settled \"no APK\" answer still offers a retry that cannot work")
	}
}

func TestRowOffersRetryAfterATransientFailure(t *testing.T) {
	vm, meta := resourceFixtureWithMeta(t, map[string]core.AppMeta{})
	meta.setStatus("com.foo", core.AppMetaStatus{State: core.AppMetaFailed, Reason: core.ReasonAPKUnreadable})

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText(core.LabelReadFailed) {
		t.Fatalf("a failed read says nothing; texts = %v", tt.Texts())
	}
	if err := tt.Click("重试读取应用信息"); err != nil {
		t.Fatalf("no retry button on a transient failure; texts = %v", tt.Texts())
	}
	deadline := time.Now().Add(2 * time.Second)
	for !containsString(meta.refreshedPackages(), "com.foo") && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !containsString(meta.refreshedPackages(), "com.foo") {
		t.Fatalf("the retry button queued %v", meta.refreshedPackages())
	}
}

func TestRowStillOffersResolveWhenNothingIsKnown(t *testing.T) {
	vm, _ := resourceFixtureWithMeta(t, map[string]core.AppMeta{})

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("解析应用信息") {
		t.Fatalf("a row nobody asked about lost its button; texts = %v", tt.Texts())
	}
}

// TestProcessViewScrollsItsOwnTable pins the process view's layout: the table
// takes the room the page leaves it and scrolls its own rows, instead of
// spilling past the window's bottom edge.
func TestProcessViewScrollsItsOwnTable(t *testing.T) {
	vm := manyAppsViewModel(t, 40)
	profile := vm.Manager.Profiles()[0]
	vm.resourceStateFor(profile.ID).view = resourceViewProcesses
	const window = 700
	tt := ui.NewTester(vm.ContentView, 900, window)
	tt.Frame()
	rect, ok := tt.Find("按进程列出的进程表")
	if !ok {
		t.Fatalf("the process view has no table; texts = %v", tt.Texts())
	}
	if bottom := rect.Y + rect.H; bottom > window-20 {
		t.Fatalf("the process table ends at %v, past the page's bottom padding", bottom)
	}
	if tt.HasText("PID 1039") {
		t.Fatal("the table built a row past the ones it shows")
	}
}
