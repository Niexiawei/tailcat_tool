package app

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
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
	// view is which of the two tables the page shows; each keeps its own table
	// state so columns and scroll do not carry over.
	view           int
	tableApps      ui.ListState
	tableProcesses ui.ListState
	// appSort and procSort are what each table's header writes; the rows are
	// ordered by them before the table is built, so the order is ours to
	// explain and to test.
	appSort  ui.SortOrder
	procSort ui.SortOrder
	expanded map[string]bool
	// batch is what a manual refresh is reading, and batchAt is when it
	// started: a package counts as done once its status changed after that, so
	// the toolbar can say how far along it is instead of guessing from a timer.
	batch   []string
	batchAt time.Time
	// filter narrows both tables: one box serves the application view and the
	// process view, so switching views does not lose the question. It is what
	// the box applied, not what it holds: filterDraft is the field's text, so
	// the tables narrow on Enter or on a Clear, never on a keystroke.
	filter      string
	filterDraft string
}

// resourceStateFor returns one device's page state, creating it on first use.
func (vm *ViewModel) resourceStateFor(device uuid.UUID) *resourceState {
	if vm.resources == nil {
		vm.resources = map[uuid.UUID]*resourceState{}
	}
	state, ok := vm.resources[device]
	if !ok {
		state = &resourceState{
			expanded: map[string]bool{},
			// Both views open on the application name: the page is about
			// which application is doing the work, not which process burns
			// the most CPU this second.
			appSort:  ui.SortOrder{Column: "app"},
			procSort: ui.SortOrder{Column: "app"},
		}
		state.tableApps.Sort = &state.appSort
		state.tableProcesses.Sort = &state.procSort
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
		if state.view == resourceViewProcesses {
			vm.processList(c, state, snapshot)
			return
		}
		vm.processTable(c, state, profile.ID, snapshot)
	})
}

// tableToolbar is the row above the table: what it lists, and the only place a
// user can force the names and icons to be read again.
func (vm *ViewModel) tableToolbar(c *ui.Context, state *resourceState, profile core.Profile, session core.Session, snapshot core.ResourceSnapshot) {
	theme := c.Theme()
	apps, processes := viewCounts(state, snapshot)
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			components.Tabs(c, &state.view, components.TabsSegmented, "应用", "进程")
			box := ui.SearchField(c, &state.filterDraft).
				Placeholder("筛选应用或进程").
				Label("筛选应用或进程").
				Width(200)
			if box.Submitted() || (box.Changed() && state.filterDraft == "") {
				state.filter = state.filterDraft
			}
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
			// The page reports progress over exactly what will be read, which
			// is the manager's answer and not the row count: processes with no
			// APK are skipped and would otherwise keep the button busy.
			targets := vm.Manager.RefreshTargets(profile.ID)
			if len(targets) == 0 {
				vm.Manager.SetNotice("列表里没有可读取的应用包")
				return
			}
			state.batch = targets
			state.batchAt = time.Now()
			go func() { vm.Fail(vm.Manager.RefreshAppMeta(context.Background(), profile.ID)) }()
			c.Invalidate()
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			title, counts := "按应用分组", itoa(apps)+" 个应用 · "+itoa(processes)+" 个进程"
			if state.view == resourceViewProcesses {
				title, counts = "按进程列出", itoa(processes)+" 个进程 · "+itoa(apps)+" 个应用"
			}
			components.Text(c, title).FontSize(13).Bold()
			components.Text(c, counts).FontSize(11).TextColor(theme.TextMuted)
		})
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

// processTable lists one row per application and, when the user expands it, its
// processes underneath. While a query is active the groups are open: what the
// user is looking for is a process inside one of them.
func (vm *ViewModel) processTable(c *ui.Context, state *resourceState, device uuid.UUID, snapshot core.ResourceSnapshot) {
	filtering := filtering(state)
	kept := sortApps(filterApps(snapshot.Apps, state.filter), state.appSort)
	if filtering && len(kept) == 0 {
		vm.noFilterMatch(c, "没有匹配的应用")
		return
	}
	rows := visibleRows(kept, filtering, state.expanded)
	tableCard(c, &state.tableApps, processColumns(), len(rows), tableOptions{
		Label: "按应用分组的进程表", Grow: true,
	}, func(row, col int) {
		line := rows[row]
		if line.process == nil {
			vm.appCell(c, col, state, device, line.app, appLabel(line.app), line.expanded)
			return
		}
		vm.processCell(c, col, *line.process)
	})
}

// appRow is one row of the grouped table: an application, or one of its
// processes when the group is open.
type appRow struct {
	app      core.AppUsage
	process  *core.ProcessUsage
	expanded bool
}

// visibleRows flattens the kept applications into the table's rows. A query
// opens every group it kept: the row that matched is a process underneath it,
// or the whole application the user asked for.
func visibleRows(kept []filteredApp, filtering bool, expanded map[string]bool) []appRow {
	rows := make([]appRow, 0, len(kept))
	for _, item := range kept {
		app := item.app
		open := filtering || expanded[app.Package]
		rows = append(rows, appRow{app: app, expanded: open})
		if !open {
			continue
		}
		processes := app.Processes
		if filtering && !item.full {
			processes = item.processes
		}
		for i := range processes {
			rows = append(rows, appRow{app: app, process: &processes[i], expanded: open})
		}
	}
	return rows
}

// filtering reports whether the box holds a question, as opposed to blanks the
// user left behind.
func filtering(state *resourceState) bool {
	return strings.TrimSpace(state.filter) != ""
}

// noFilterMatch explains an empty table instead of leaving the area blank.
func (vm *ViewModel) noFilterMatch(c *ui.Context, what string) {
	theme := c.Theme()
	components.Text(c, what).FontSize(12).TextColor(theme.TextMuted)
	components.Text(c, "清空筛选即可看到全部").FontSize(11).TextColor(theme.TextMuted)
}

// viewCounts is what the toolbar says the table holds, after the filter.
func viewCounts(state *resourceState, snapshot core.ResourceSnapshot) (apps, processes int) {
	active := filtering(state)
	if state.view == resourceViewProcesses {
		seen := map[string]bool{}
		for _, row := range filterProcessRows(processRows(snapshot), state.filter) {
			processes++
			seen[row.pkg] = true
		}
		return len(seen), processes
	}
	for _, item := range filterApps(snapshot.Apps, state.filter) {
		apps++
		if !active || item.full {
			processes += len(item.app.Processes)
			continue
		}
		processes += len(item.processes)
	}
	return apps, processes
}

// processColumns are the table's columns. The order is fixed: the page sorts by
// CPU, so a click on a header would fight the next reading.
func processColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "应用", ID: "app", MinWidth: 200, Sortable: true},
		{Title: "进程", ID: "procs", Width: 80, Align: ui.End, Sortable: true},
		{Title: "CPU", ID: "cpu", Width: 72, Align: ui.End, Sortable: true},
		{Title: "GPU", ID: "gpu", Width: 72, Align: ui.End, Sortable: true},
		{Title: "内存", ID: "memory", Width: 88, Align: ui.End, Sortable: true},
	}
}

func (vm *ViewModel) appCell(c *ui.Context, col int, state *resourceState, device uuid.UUID, app core.AppUsage, label string, expanded bool) {
	theme := c.Theme()
	switch col {
	case 0:
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			icon, action := lucide.ChevronRight(), "展开 "+label
			if expanded {
				icon, action = lucide.ChevronDown(), "收起 "+label
			}
			if components.IconButton(c, icon, action, components.IconButtonCompact).Clicked() {
				state.expanded[app.Package] = !expanded
			}
			components.Avatar(c, label, components.AvatarOptions{Image: vm.appIcon(device, app.Package), Size: 20})
			components.Text(c, label).FontSize(12).SingleLine()
			if app.MetaStale {
				components.Text(c, "7 天前更新").FontSize(10).TextColor(theme.TextMuted)
			}
			vm.appMetaAction(c, device, app)
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

// appMetaAction is the end of every row's name cell: a spinner while the store
// says a read is running, the reason an unnamed row failed, or the button that
// asks for a read. The state comes from the store, never from a timer here: a
// blind two-minute wait is why a row whose package does not exist looked busy
// forever.
//
// The button survives the first read. An application that renamed or re-iconed
// itself leaves a cached name that looks right, so the only way to notice is to
// read it again; the row offers that without going through 缓存管理.
func (vm *ViewModel) appMetaAction(c *ui.Context, device uuid.UUID, app core.AppUsage) {
	theme := c.Theme()
	pkg := app.Package
	status, known := vm.Manager.AppMetaStatus(device, pkg)
	if known && status.State == core.AppMetaPending {
		components.Spinner(c, components.SpinnerRing).Width(14).Height(14).Label("正在读取应用信息")
		return
	}
	if app.Label != "" {
		// The cached name is still what we show; a failed refresh keeps it and
		// leaves the button in place to try again.
		if components.IconButton(c, lucide.RefreshCw(), "重新读取应用信息", components.IconButtonCompact).Clicked() {
			vm.askAppMeta(c, device, pkg)
		}
		return
	}
	if known && status.State == core.AppMetaFailed {
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
		return
	}
	if components.IconButton(c, lucide.PackageSearch(), "解析应用信息", components.IconButtonCompact).Clicked() {
		vm.askAppMeta(c, device, pkg)
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

// forgetAppIcons drops one device's decoded icons, so a cleared cache is not
// contradicted by an image this process still holds.
func (vm *ViewModel) forgetAppIcons(device uuid.UUID) {
	prefix := device.String() + "|"
	for key := range vm.appIcons {
		if strings.HasPrefix(key, prefix) {
			delete(vm.appIcons, key)
		}
	}
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

// processList lists one process per row: the same numbers as the grouped view,
// without folding them under the application.
func (vm *ViewModel) processList(c *ui.Context, state *resourceState, snapshot core.ResourceSnapshot) {
	rows := sortProcessRows(filterProcessRows(processRows(snapshot), state.filter), state.procSort)
	if filtering(state) && len(rows) == 0 {
		vm.noFilterMatch(c, "没有匹配的进程")
		return
	}
	tableCard(c, &state.tableProcesses, processViewColumns(), len(rows), tableOptions{
		Label: "按进程列出的进程表", Grow: true,
	}, func(row, col int) {
		vm.processRowCell(c, col, rows[row])
	})
}

// processViewColumns keeps the process view's columns apart from the grouped
// view's: the application moves to the second place and the process to the
// first, because that is what the rows are about.
func processViewColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "进程", ID: "proc", MinWidth: 200, Sortable: true},
		{Title: "应用", ID: "app", MinWidth: 160, Sortable: true},
		{Title: "CPU", ID: "cpu", Width: 72, Align: ui.End, Sortable: true},
		{Title: "GPU", ID: "gpu", Width: 72, Align: ui.End, Sortable: true},
		{Title: "内存", ID: "memory", Width: 88, Align: ui.End, Sortable: true},
	}
}

// processRow is one process with the application it belongs to.
type processRow struct {
	id      string
	process core.ProcessUsage
	app     string
	pkg     string
}

// processRows flattens the grouped reading into one row per process. The order
// is the page's: sortProcessRows puts the rows in the order the table's header
// asks for.
func processRows(snapshot core.ResourceSnapshot) []processRow {
	rows := make([]processRow, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		for _, process := range app.Processes {
			rows = append(rows, processRow{
				id:      app.Package + "#" + itoa(process.PID),
				process: process,
				app:     appLabel(app),
				pkg:     app.Package,
			})
		}
	}
	return rows
}

func (vm *ViewModel) processRowCell(c *ui.Context, col int, row processRow) {
	theme := c.Theme()
	switch col {
	case 0:
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			components.Text(c, row.process.Name).FontSize(11).SingleLine()
			components.Text(c, "PID "+itoa(row.process.PID)).FontSize(10).TextColor(theme.TextMuted)
		})
	case 1:
		components.Text(c, row.app).FontSize(11).TextColor(theme.TextMuted).SingleLine()
	case 2:
		components.Text(c, percent1Text(row.process.CPUPercent)).FontSize(11).TextColor(theme.Text)
	case 3:
		components.Text(c, gpuCellText(row.process.GPUOk, row.process.GPUPercent)).FontSize(11).TextColor(theme.Text)
	default:
		components.Text(c, core.FormatBytes(row.process.MemBytes)).FontSize(11).TextColor(theme.Text)
	}
}

// sortApps orders the grouped rows by order: application name ascending when it
// names no column of the application view. A descending order turns only the
// column's own key; the name each row falls back to stays ascending, so the
// rows an application groups stay together.
func sortApps(rows []filteredApp, order ui.SortOrder) []filteredApp {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		primary := 0
		switch order.Column {
		case "procs":
			primary = compareInts(len(a.app.Processes), len(b.app.Processes))
		case "cpu":
			primary = compareFloats(a.app.CPUPercent, b.app.CPUPercent)
		case "gpu":
			// 不可用 is not a number: it stays last whichever way the column
			// sorts, so a device without counters never reads as 0%.
			if cmp := compareGPUs(a.app.GPUOk, a.app.GPUPercent, b.app.GPUOk, b.app.GPUPercent); cmp != 0 {
				return cmp < 0
			}
			return compareAppNames(a, b) < 0
		case "memory":
			primary = compareUints(a.app.MemBytes, b.app.MemBytes)
		default:
			primary = compareAppNames(a, b)
		}
		if primary == 0 {
			return compareAppNames(a, b) < 0
		}
		return (primary < 0) != order.Descending
	})
	return rows
}

// sortProcessRows orders the flat rows by order: application name ascending when
// it names no column of the process view. The application is the first key under
// every column, so one application's processes stay together.
func sortProcessRows(rows []processRow, order ui.SortOrder) []processRow {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		primary := 0
		switch order.Column {
		case "proc":
			primary = compareProcessNames(a, b)
		case "cpu":
			primary = compareFloats(a.process.CPUPercent, b.process.CPUPercent)
		case "gpu":
			if cmp := compareGPUs(a.process.GPUOk, a.process.GPUPercent, b.process.GPUOk, b.process.GPUPercent); cmp != 0 {
				return cmp < 0
			}
			return compareRows(a, b) < 0
		case "memory":
			primary = compareUints(a.process.MemBytes, b.process.MemBytes)
		default:
			primary = compareRowApps(a, b)
		}
		if primary == 0 {
			return compareRows(a, b) < 0
		}
		return (primary < 0) != order.Descending
	})
	return rows
}

// compareGPUs orders a device without GPU counters after the ones with them,
// whatever the direction: the cell reads 不可用, and putting that above a
// measured 0% would claim a usage the device does not report.
func compareGPUs(okA bool, a float64, okB bool, b float64) int {
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return 1
	case !okB:
		return -1
	}
	return compareFloats(a, b)
}

// compareAppNames puts applications in the order their names read.
func compareAppNames(a, b filteredApp) int {
	if cmp := strings.Compare(strings.ToLower(appLabel(a.app)), strings.ToLower(appLabel(b.app))); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.app.Package, b.app.Package)
}

// compareProcessNames puts processes in the order their names read.
func compareProcessNames(a, b processRow) int {
	return strings.Compare(strings.ToLower(a.process.Name), strings.ToLower(b.process.Name))
}

// compareRowApps keeps one application's rows together, then puts them in name
// order inside it.
func compareRowApps(a, b processRow) int {
	if cmp := strings.Compare(strings.ToLower(a.app), strings.ToLower(b.app)); cmp != 0 {
		return cmp
	}
	return compareProcessNames(a, b)
}

// compareRows is the last tie-breaker: the row's own name, then its process id,
// so equal numbers never swap places between samples.
func compareRows(a, b processRow) int {
	if cmp := compareProcessNames(a, b); cmp != 0 {
		return cmp
	}
	return compareInts(a.process.PID, b.process.PID)
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareUints(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
