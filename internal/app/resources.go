package app

import (
	"strconv"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/chart"
)

// resourceState is the resource page's own state, kept per device so switching
// devices does not carry a chart selection or an expanded group across.
type resourceState struct {
	cpu, mem, gpu chart.State
	table         components.TableState
	expanded      map[string]bool
	scroll        ui.ScrollState
	// refreshing is set while a manual refresh runs, so the button cannot be
	// pressed twice and the user sees why nothing changed yet.
	refreshing bool
}

// resourceStateFor returns one device's page state, creating it on first use.
func (vm *ViewModel) resourceStateFor(device uuid.UUID) *resourceState {
	if vm.resources == nil {
		vm.resources = map[uuid.UUID]*resourceState{}
	}
	state, ok := vm.resources[device]
	if !ok {
		state = &resourceState{
			// The donuts show the used slice by default, so the number in the
			// middle is the percentage rather than the ring's total.
			cpu:      chart.State{Index: 0},
			mem:      chart.State{Index: 0},
			gpu:      chart.State{Index: 0},
			expanded: map[string]bool{},
		}
		vm.resources[device] = state
	}
	return state
}

// resourcePage draws the resource tab: hardware identity, three usage rings and
// the grouped process table.
func (vm *ViewModel) resourcePage(c *ui.Context, profile core.Profile, session core.Session) {
	if session.State != core.StateOnline {
		vm.resourceOffline(c, session)
		return
	}
	// Sampling runs only while this page is visible; leaving the tab stops it.
	vm.Manager.StartResourceSampling(profile.ID)
	snapshot, ok := vm.Manager.Resources(profile.ID)
	if !ok {
		ui.Column(c).FillWidth().Padding(0, 2).Children(func() {
			components.Text(c, "正在读取设备资源…").FontSize(11).TextColor(c.Theme().TextMuted)
		})
		return
	}

	state := vm.resourceStateFor(profile.ID)
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		vm.hardwareLine(c, snapshot)
		vm.donutRow(c, state, snapshot)
		vm.warnings(c, snapshot)
	})
}

// resourceOffline says why there is nothing to show instead of drawing empty
// charts, which would read as zero usage.
func (vm *ViewModel) resourceOffline(c *ui.Context, session core.Session) {
	theme := c.Theme()
	reason := "连接后才能读取设备的资源占用。"
	if session.Err != "" {
		reason = session.Err
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		components.Text(c, "设备未连接").FontSize(13).Bold()
		components.Text(c, reason).FontSize(11).TextColor(theme.TextMuted)
	})
}

// hardwareLine names what the numbers below were measured on.
func (vm *ViewModel) hardwareLine(c *ui.Context, snapshot core.ResourceSnapshot) {
	ui.Row(c).FillWidth().Gap(24).Children(func() {
		vm.hardwareItem(c, "CPU", cpuHardwareText(snapshot.CPU))
		vm.hardwareItem(c, "GPU", gpuHardwareText(snapshot.GPU))
		vm.hardwareItem(c, "内存", memoryText(snapshot.Memory))
	})
}

func (vm *ViewModel) hardwareItem(c *ui.Context, label, value string) {
	ui.Column(c).Gap(2).MinWidth(0).Children(func() {
		components.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		components.Text(c, value).FontSize(12).SingleLine()
	})
}

// donutRow draws the three rings. GPU is the one vendors disagree about, so an
// unavailable counter is said out loud rather than drawn as an empty ring.
func (vm *ViewModel) donutRow(c *ui.Context, state *resourceState, snapshot core.ResourceSnapshot) {
	ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
		vm.usageRing(c, "CPU 占用", &state.cpu, snapshot.CPU.Percent, snapshot.CPU.Available)
		vm.usageRing(c, "内存占用", &state.mem, snapshot.Memory.Percent, snapshot.Memory.OK)
		if snapshot.GPU.Available {
			vm.usageRing(c, "GPU 占用", &state.gpu, snapshot.GPU.Percent, true)
			return
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			components.Text(c, "GPU 占用").FontSize(11).TextColor(c.Theme().TextMuted)
			components.Text(c, "不可用").FontSize(20).Bold()
			components.Text(c, core.WarningNoGPU).FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}

func (vm *ViewModel) usageRing(c *ui.Context, title string, state *chart.State, percent float64, available bool) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		components.Text(c, title).FontSize(11).TextColor(c.Theme().TextMuted)
		if !available {
			// The first reading has no delta yet: "—" is honest, 0% is not.
			components.Text(c, core.PlaceholderUnknown).FontSize(20).Bold()
			return
		}
		used := clampPercent(percent)
		chart.Donut(c, state, []chart.Slice{
			{ID: "used", Label: "已用", Value: used},
			{ID: "idle", Label: "空闲", Value: 100 - used},
		}, chart.Options{
			Label:       title,
			PlotHeight:  150,
			FormatValue: percentText,
		})
	})
}

// warnings prints what this reading could not fill in. The GPU warning is
// already spelled out beside its ring, so it is skipped here.
func (vm *ViewModel) warnings(c *ui.Context, snapshot core.ResourceSnapshot) {
	for _, warning := range snapshot.Warnings {
		if warning == core.WarningNoGPU {
			continue
		}
		components.Text(c, warning).FontSize(11).TextColor(c.Theme().TextMuted)
	}
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func percentText(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64) + "%"
}

func cpuHardwareText(cpu core.CPUSample) string {
	model := cpu.Model
	if model == "" {
		model = core.PlaceholderUnknown
	}
	if cpu.Cores <= 0 {
		return model
	}
	return model + " · " + strconv.Itoa(cpu.Cores) + " 核"
}

func gpuHardwareText(gpu core.GPUSample) string {
	if gpu.Renderer == "" {
		return core.PlaceholderUnknown
	}
	return gpu.Renderer
}

func memoryText(memory core.MemorySample) string {
	if !memory.OK || memory.Total == 0 {
		return core.PlaceholderUnknown
	}
	return core.FormatBytes(memory.Used) + " / " + core.FormatBytes(memory.Total)
}
