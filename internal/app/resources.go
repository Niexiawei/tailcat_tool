package app

import (
	"context"
	"strconv"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/chart"
	"github.com/oneo-me/onevra/source/icons/lucide"
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
	// refreshAt and refreshSeen are what tells the page a refresh finished:
	// a newer reading, or a different number of named applications.
	refreshAt   time.Time
	refreshSeen int
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
	ui.Column(c).Fill().Gap(12).Children(func() {
		vm.hardwareLine(c, snapshot)
		vm.donutRow(c, state, snapshot)
		vm.warnings(c, snapshot)
		vm.processSection(c, state, profile, session, snapshot)
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

// processSection is the toolbar plus the grouped process table.
func (vm *ViewModel) processSection(c *ui.Context, state *resourceState, profile core.Profile, session core.Session, snapshot core.ResourceSnapshot) {
	vm.settleRefresh(state, snapshot)
	ui.Column(c).FillWidth().Grow(1).Gap(6).Children(func() {
		vm.tableToolbar(c, state, profile, session, snapshot)
		vm.processTable(c, state, profile.ID, snapshot)
	})
}

// tableToolbar is the row above the table: what it lists, and the only place a
// user can force the names and icons to be read again.
func (vm *ViewModel) tableToolbar(c *ui.Context, state *resourceState, profile core.Profile, session core.Session, snapshot core.ResourceSnapshot) {
	theme := c.Theme()
	processes := 0
	for _, app := range snapshot.Apps {
		processes += len(app.Processes)
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		components.Text(c, "按应用分组").FontSize(13).Bold()
		components.Text(c, itoa(len(snapshot.Apps))+" 个应用 · "+itoa(processes)+" 个进程").
			FontSize(11).TextColor(theme.TextMuted)
		ui.Box(c).Grow(1)
		if state.refreshing {
			components.Text(c, "正在刷新应用信息…").FontSize(11).TextColor(theme.TextMuted)
		}
		button := components.IconButton(c, lucide.RefreshCw(), "刷新应用信息", components.IconButtonCompact).
			Disabled(state.refreshing || session.State != core.StateOnline)
		if !button.Clicked() {
			return
		}
		state.refreshing = true
		state.refreshAt = snapshot.At
		state.refreshSeen = namedApps(snapshot)
		go func() { vm.Fail(vm.Manager.RefreshAppMeta(context.Background(), profile.ID)) }()
		c.Invalidate()
	})
}

// settleRefresh ends the "refreshing" state once a newer reading arrived or the
// number of named applications changed. Waiting for every application would
// never end: system binaries have no APK to read.
func (vm *ViewModel) settleRefresh(state *resourceState, snapshot core.ResourceSnapshot) {
	if !state.refreshing {
		return
	}
	if namedApps(snapshot) != state.refreshSeen || snapshot.At.After(state.refreshAt) || time.Since(state.refreshAt) > 30*time.Second {
		state.refreshing = false
	}
}

func namedApps(snapshot core.ResourceSnapshot) int {
	named := 0
	for _, app := range snapshot.Apps {
		if app.Label != "" {
			named++
		}
	}
	return named
}

// processTable lists one row per application and, when the user expands it, its
// processes underneath.
func (vm *ViewModel) processTable(c *ui.Context, state *resourceState, device uuid.UUID, snapshot core.ResourceSnapshot) {
	components.ScrollView(c, components.ScrollViewOptions{}).FillWidth().Grow(1).Children(func() {
		components.Table(c, &state.table, processColumns(), func() {
			for _, app := range snapshot.Apps {
				label := appLabel(app)
				row := components.TableRow(c, app.Package, label, state.expanded[app.Package], func(col int) {
					vm.appCell(c, col, device, app, label)
				})
				if row.Clicked() {
					state.expanded[app.Package] = !state.expanded[app.Package]
				}
				if !state.expanded[app.Package] {
					continue
				}
				for _, process := range app.Processes {
					components.TableRow(c, app.Package+"#"+itoa(process.PID), "  "+process.Name, false, func(col int) {
						vm.processCell(c, col, process)
					})
				}
			}
		})
	})
}

// processColumns are the table's columns. The order is fixed: the page sorts by
// CPU, so a click on a header would fight the next reading.
func processColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "应用", ID: "app", MinWidth: 200},
		{Title: "进程", ID: "procs", Width: 80, Align: ui.End},
		{Title: "CPU", ID: "cpu", Width: 72, Align: ui.End},
		{Title: "GPU", ID: "gpu", Width: 72, Align: ui.End},
		{Title: "内存", ID: "memory", Width: 88, Align: ui.End},
	}
}

func (vm *ViewModel) appCell(c *ui.Context, col int, device uuid.UUID, app core.AppUsage, label string) {
	theme := c.Theme()
	switch col {
	case 0:
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			components.Avatar(c, label, components.AvatarOptions{Image: vm.appIcon(device, app.Package), Size: 20})
			components.Text(c, label).FontSize(12).SingleLine()
			if app.MetaStale {
				components.Text(c, "7 天前更新").FontSize(10).TextColor(theme.TextMuted)
			}
		})
	case 1:
		components.Text(c, itoa(len(app.Processes))+" 个进程").FontSize(11).TextColor(theme.TextMuted)
	case 2:
		components.Text(c, percent1Text(app.CPUPercent)).FontSize(11).TextColor(theme.Text)
	case 3:
		components.Text(c, gpuCellText(app.GPUOk, app.GPUPercent)).FontSize(11).TextColor(theme.Text)
	default:
		components.Text(c, core.FormatBytes(app.MemBytes)).FontSize(11).TextColor(theme.Text)
	}
}

func (vm *ViewModel) processCell(c *ui.Context, col int, process core.ProcessUsage) {
	theme := c.Theme()
	switch col {
	case 0:
		components.Text(c, "  "+process.Name).FontSize(11).TextColor(theme.TextMuted).SingleLine()
	case 1:
		components.Text(c, "PID "+itoa(process.PID)).FontSize(11).TextColor(theme.TextMuted)
	case 2:
		components.Text(c, percent1Text(process.CPUPercent)).FontSize(11).TextColor(theme.TextMuted)
	case 3:
		components.Text(c, gpuCellText(process.GPUOk, process.GPUPercent)).FontSize(11).TextColor(theme.TextMuted)
	default:
		components.Text(c, core.FormatBytes(process.MemBytes)).FontSize(11).TextColor(theme.TextMuted)
	}
}

// appLabel is what the group row shows: the application's own name once it is
// cached, the package name until then.
func appLabel(app core.AppUsage) string {
	if app.Label != "" {
		return app.Label
	}
	return app.Package
}

// gpuCellText never prints 0% for a device without GPU counters.
func gpuCellText(available bool, percent float64) string {
	if !available {
		return "不可用"
	}
	return percent1Text(percent)
}

func percent1Text(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}

// iconAsset is one decoded icon and the reading it came from, so a refetched
// application shows its new icon instead of the one decoded an hour ago.
type iconAsset struct {
	fetchedAt time.Time
	image     ui.ImageSource
}

// appIcon decodes one application's icon once per reading and remembers it,
// including the answer "there is none", which Avatar draws as initials.
func (vm *ViewModel) appIcon(device uuid.UUID, pkg string) ui.ImageSource {
	if vm.appIcons == nil {
		vm.appIcons = map[string]iconAsset{}
	}
	key := device.String() + "|" + pkg
	var fetchedAt time.Time
	var icon []byte
	if meta, ok := vm.Manager.AppMeta(device, pkg); ok {
		fetchedAt, icon = meta.FetchedAt, meta.Icon
	}
	if cached, ok := vm.appIcons[key]; ok && cached.fetchedAt.Equal(fetchedAt) {
		return cached.image
	}
	var image ui.ImageSource
	if len(icon) > 0 {
		if decoded, err := ui.DecodeImage(icon); err == nil {
			image = decoded
		}
	}
	vm.appIcons[key] = iconAsset{fetchedAt: fetchedAt, image: image}
	return image
}
