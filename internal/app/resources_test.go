package app

import (
	"context"
	"strings"
	"testing"

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
	return b.String()
}

// resourceFixture connects a device, runs two sampling rounds and leaves the
// resource tab selected, so the page has numbers to draw.
func resourceFixture(t *testing.T) (*ViewModel, func() []string) {
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
	})
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
