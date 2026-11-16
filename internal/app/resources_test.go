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
	b.WriteString("@@CPU\n8\nHardware\t: Qualcomm Technologies, Inc SM8150\n")
	b.WriteString("@@GPU\n" + gpu + "\n")
	b.WriteString("@@PROC\n")
	b.WriteString("@@P 100\n")
	b.WriteString("100 (comm) S 1 100 100 0 -1 0 0 0 0 0 " + itoa(int(utime)) + " 0 0 0 20 0 1 0 500 0 0\n")
	b.WriteString("VmRSS:\t   20480 kB\n")
	b.WriteString("com.foo \n")
	b.WriteString("@@P 101\n")
	b.WriteString("101 (comm) S 1 101 101 0 -1 0 0 0 0 0 5 0 0 0 20 0 1 0 600 0 0\n")
	b.WriteString("VmRSS:\t   1024 kB\n")
	b.WriteString("com.foo:remote \n")
	b.WriteString("@@P 200\n")
	b.WriteString("200 (comm) S 1 200 200 0 -1 0 0 0 0 0 1 0 0 0 20 0 1 0 700 0 0\n")
	b.WriteString("VmRSS:\t   4096 kB\n")
	b.WriteString("system_server \n")
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
	if _, ok := vm.Manager.Resources(profile.ID); ok {
		t.Fatal("the closed tab kept its sampling loop and reading")
	}
}

// stubAppMeta is the metadata cache as the view sees it.
type stubAppMeta struct {
	mu        sync.Mutex
	metas     map[string]core.AppMeta
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

func TestProcessTableExpandsOnClick(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("PID 100") {
		t.Fatal("the process rows are visible before expanding the group")
	}
	if err := tt.Click("com.foo"); err != nil {
		t.Fatalf("clicking the group row: %v", err)
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("PID 100") || !tt.HasText("com.foo:remote") {
		t.Fatalf("the expanded group does not show its processes; texts = %v", tt.Texts())
	}
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
	if !vm.resourceStateFor(profile.ID).refreshing {
		t.Fatal("the page does not show that a refresh is running")
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
