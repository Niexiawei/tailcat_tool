package app

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// resourceState is the resource page's own state, kept per device so switching
// devices does not carry an expanded group across.
type resourceState struct {
	table    components.TableState
	expanded map[string]bool
	scroll   ui.ScrollState
	// batch is what a manual refresh is reading, and batchAt is when it
	// started: a package counts as done once its status changed after that, so
	// the toolbar can say how far along it is instead of guessing from a timer.
	batch   []string
	batchAt time.Time
}

// resourceStateFor returns one device's page state, creating it on first use.
func (vm *ViewModel) resourceStateFor(device uuid.UUID) *resourceState {
	if vm.resources == nil {
		vm.resources = map[uuid.UUID]*resourceState{}
	}
	state, ok := vm.resources[device]
	if !ok {
		state = &resourceState{expanded: map[string]bool{}}
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
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(12).Children(func() {
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

// hardwareLine names what the numbers below were measured on. The three share
// the width equally and truncate, so a long CPU model cannot push the memory
// number off the page.
func (vm *ViewModel) hardwareLine(c *ui.Context, snapshot core.ResourceSnapshot) {
	ui.Row(c).FillWidth().Shrink(0).Gap(16).Children(func() {
		vm.hardwareItem(c, "CPU", cpuHardwareText(snapshot.CPU))
		vm.hardwareItem(c, "GPU", gpuHardwareText(snapshot.GPU))
		vm.hardwareItem(c, "内存", memoryText(snapshot.Memory))
	})
}

func (vm *ViewModel) hardwareItem(c *ui.Context, label, value string) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
		components.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		components.Tooltip(c, components.Text(c, value).FontSize(12).SingleLine(), value)
	})
}

// donutRow draws the three rings. GPU is the one vendors disagree about, so an
// unavailable counter is said out loud rather than drawn as an empty ring.
func (vm *ViewModel) donutRow(c *ui.Context, state *resourceState, snapshot core.ResourceSnapshot) {
	ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
		vm.usageRing(c, "CPU 占用", snapshot.CPU.Percent, snapshot.CPU.Available)
		vm.usageRing(c, "内存占用", snapshot.Memory.Percent, snapshot.Memory.OK)
		if snapshot.GPU.Available {
			vm.usageRing(c, "GPU 占用", snapshot.GPU.Percent, true)
			return
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			components.Text(c, "GPU 占用").FontSize(11).TextColor(c.Theme().TextMuted)
			components.Text(c, "不可用").FontSize(20).Bold()
			components.Text(c, core.WarningNoGPU).FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}

// usageRing is one share of the whole: a thick ring with the usage written in
// the middle. components.ProgressRing paints the same thing with a stroke of
// theme.Space(0.75) and offers no way to thicken it, which reads as a hairline
// at this size, so the ring is painted here — one place, same semantics
// (RoleProgress with Range(0,1,value)).
func (vm *ViewModel) usageRing(c *ui.Context, title string, percent float64, available bool) {
	theme := c.Theme()
	size := theme.Space(30)
	ui.Column(c).Grow(1).MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
		components.Text(c, title).FontSize(11).TextColor(theme.TextMuted)
		if !available {
			// The first reading has no delta yet: "—" is honest, 0% is not.
			components.Text(c, core.PlaceholderUnknown).FontSize(20).Bold()
			return
		}
		used := clampPercent(percent)
		ring := ui.Box(c).Size(size, size).Shrink(0)
		ring.Role(ui.RoleProgress).Range(0, 1, used/100).Label(title)
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			d := min(r.W, r.H)
			if d <= 0 {
				return
			}
			width := d / 9
			radius := (d - width) / 2
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			var track ui.Path
			track.Circle(cx, cy, radius)
			p.StrokePath(&track, width, theme.Border)
			if used <= 0 {
				return
			}
			var fill ui.Path
			if used >= 100 {
				fill.Circle(cx, cy, radius)
			} else {
				ringArc(&fill, cx, cy, radius, used)
			}
			p.StrokePath(&fill, width, theme.Accent)
		})
		ring.Children(func() {
			ui.Column(c).Absolute().Left(0).Top(0).WidthPercent(100).HeightPercent(100).
				AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				components.Text(c, percentText(used)).FontSize(theme.FontSize * 1.3).Bold()
			})
		})
	})
}

// ringArc walks a clockwise arc from twelve o'clock, one degree per segment:
// ui.Path has circles and quadratic curves but no arc primitive.
func ringArc(path *ui.Path, cx, cy, radius float32, percent float64) {
	steps := int(max(1, min(360, percent/100*360)))
	start := -math.Pi / 2
	point := func(i int) (float32, float32) {
		angle := start + float64(i)/float64(steps)*percent/100*2*math.Pi
		return cx + float32(math.Cos(angle))*radius, cy + float32(math.Sin(angle))*radius
	}
	x, y := point(0)
	path.MoveTo(x, y)
	for i := 1; i <= steps; i++ {
		x, y = point(i)
		path.LineTo(x, y)
	}
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
	vm.settleRefresh(state, profile.ID)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(6).Children(func() {
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
		if done, total := vm.batchProgress(state, profile.ID); total > 0 {
			components.Text(c, "正在读取应用信息 "+itoa(done)+"/"+itoa(total)).
				FontSize(11).TextColor(theme.TextMuted)
		}
		button := components.IconButton(c, lucide.RefreshCw(), "刷新应用信息", components.IconButtonCompact).
			Disabled(len(state.batch) > 0 || session.State != core.StateOnline)
		if !button.Clicked() {
			return
		}
		state.batch = appPackages(snapshot)
		state.batchAt = time.Now()
		go func() { vm.Fail(vm.Manager.RefreshAppMeta(context.Background(), profile.ID)) }()
		c.Invalidate()
	})
}

// settleRefresh ends the refresh once every package it asked about has settled.
// The 90-second guard is the only timer left on this page, and it exists for the
// one case the statuses cannot cover: a read that never reports back at all.
func (vm *ViewModel) settleRefresh(state *resourceState, device uuid.UUID) {
	if len(state.batch) == 0 {
		return
	}
	if done, total := vm.batchProgress(state, device); done >= total || time.Since(state.batchAt) > batchGiveUpAfter {
		state.batch = nil
	}
}

// batchProgress counts the packages of a running refresh that reached a
// terminal state after it started.
func (vm *ViewModel) batchProgress(state *resourceState, device uuid.UUID) (done, total int) {
	for _, pkg := range state.batch {
		total++
		status, known := vm.Manager.AppMetaStatus(device, pkg)
		if !known || status.State == core.AppMetaPending || status.State == core.AppMetaUnknown {
			continue
		}
		if status.At.Before(state.batchAt) {
			continue
		}
		done++
	}
	return done, total
}

// batchGiveUpAfter is how long a refresh may keep the toolbar busy without
// progress. It never hides a row's own state; it only re-enables one button.
const batchGiveUpAfter = 90 * time.Second

func appPackages(snapshot core.ResourceSnapshot) []string {
	pkgs := make([]string, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		pkgs = append(pkgs, app.Package)
	}
	return pkgs
}

// processTable lists one row per application and, when the user expands it, its
// processes underneath.
func (vm *ViewModel) processTable(c *ui.Context, state *resourceState, device uuid.UUID, snapshot core.ResourceSnapshot) {
	components.ScrollView(c, components.ScrollViewOptions{}).FillWidth().Grow(1).MinHeight(0).Label("按应用分组的进程表").Children(func() {
		components.Table(c, &state.table, processColumns(), func() {
			for _, app := range snapshot.Apps {
				label := appLabel(app)
				row := components.TableRow(c, app.Package, label, state.expanded[app.Package], func(col int) {
					vm.appCell(c, col, state, device, app, label)
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

func (vm *ViewModel) appCell(c *ui.Context, col int, state *resourceState, device uuid.UUID, app core.AppUsage, label string) {
	theme := c.Theme()
	switch col {
	case 0:
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			components.Avatar(c, label, components.AvatarOptions{Image: vm.appIcon(device, app.Package), Size: 20})
			components.Text(c, label).FontSize(12).SingleLine()
			if app.MetaStale {
				components.Text(c, "7 天前更新").FontSize(10).TextColor(theme.TextMuted)
			}
			if app.Label == "" {
				vm.appMetaAction(c, device, app.Package)
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

// appMetaAction is the end of an unnamed row: a spinner while the store says a
// read is running, the reason it failed, or the button that asks for one. The
// state comes from the store, never from a timer here: a blind two-minute wait
// is why a row whose package does not exist looked busy forever.
func (vm *ViewModel) appMetaAction(c *ui.Context, device uuid.UUID, pkg string) {
	theme := c.Theme()
	status, known := vm.Manager.AppMetaStatus(device, pkg)
	switch {
	case known && status.State == core.AppMetaPending:
		components.Spinner(c, components.SpinnerRing).Width(14).Height(14).Label("正在读取应用信息")
	case known && status.State == core.AppMetaFailed:
		label := core.LabelReadFailed
		if status.Final {
			label = core.LabelNoAppPackage
		}
		components.Tooltip(c, components.Text(c, label).FontSize(10).TextColor(theme.TextMuted), status.Reason)
		if status.Final {
			// Asking again cannot produce an APK the device does not have.
			return
		}
		if components.IconButton(c, lucide.RefreshCw(), "重试读取应用信息", components.IconButtonCompact).Clicked() {
			vm.askAppMeta(c, device, pkg)
		}
	default:
		if components.IconButton(c, lucide.PackageSearch(), "解析应用信息", components.IconButtonCompact).Clicked() {
			vm.askAppMeta(c, device, pkg)
		}
	}
}

// askAppMeta queues one row's read. The call only queues work, so it runs on the
// render goroutine and the row shows its spinner on the very next frame instead
// of after the next two-second sampling tick.
func (vm *ViewModel) askAppMeta(c *ui.Context, device uuid.UUID, pkg string) {
	vm.Fail(vm.Manager.ResolveAppMeta(context.Background(), device, []string{pkg}))
	c.Invalidate()
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
