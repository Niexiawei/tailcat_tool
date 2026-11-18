// Package app builds the Tailcat Manager window on mygo and onevra.
package app

import (
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/oneo-me/onevra/source/shells/sidebar"
)

// The device page's tabs, in the order the page shows them.
const (
	detailOverview = iota
	detailResources
	detailTerminal
	detailFiles
	detailLog
)

// The resource page shows the same reading two ways: one row per application
// (processes fold underneath), or one row per process.
const (
	resourceViewApps = iota
	resourceViewProcesses
)

// ViewModel is everything the window reads. It owns no manager state of its
// own: the manager is the single source of truth and the window redraws when
// it says something changed.
type ViewModel struct {
	Manager  *core.Manager
	Locate   core.LocateTool
	Install  func(core.ToolComponent)
	Activate func(deviceID string)

	// History answers the log page's queries; nil keeps the page empty rather
	// than letting it touch SQL itself.
	History core.HistoryReader
	// RecordAppLog stores a line the app itself produced. The manager hands it
	// to history without blocking; nil makes it a no-op.
	RecordAppLog func(core.AppLogRecord)

	// Router owns the content pages; the sidebar and the breadcrumbs
	// navigate it, so Back returns to the page before.
	Router *ui.Router
	// CrumbChoice receives the index of a breadcrumb the user activated.
	CrumbChoice int

	// Selection is the device the detail pane shows.
	Selection uuid.UUID
	// DetailTab is the device page's current tab; see detailOverview and the
	// constants beside it.
	DetailTab int
	// ConsoleTab is ConsoleShell or ConsoleADB inside the terminal tab.
	ConsoleTab int
	// Demo marks the simulated environment.
	Demo bool
	// PendingEditor is the device form that is open, nil when closed.
	PendingEditor *EditorRequest

	// Form state. MyGo views are functions of state, so every text field is a
	// field here.
	EditorDraft EditorInput
	EditorError string

	TransferOpen       bool
	TransferUpload     bool
	TransferLocalPath  string
	TransferRemotePath string
	TransferError      string

	SettingsOpen  bool
	SettingsDraft core.ToolSettings
	SettingsError string

	ReachabilityOpen bool
	AttentionOpen    bool

	// FileDetail is the entry whose attribute dialog is open; nil closes it.
	FileDetail *FileDetail

	// Sidebar section expansion; both start open.
	SavedOpen     bool
	TemporaryOpen bool

	// Invalidate repaints the window; set once the window exists.
	Invalidate func()

	// pendingToasts is filled from any goroutine and drained while the view
	// builds, because a toast can only be added with a live context.
	toastsMu sync.Mutex
	toasts   []ui.Toast

	// CacheDevice is the device whose cached app metadata the page shows.
	CacheDevice uuid.UUID
	// CacheTable keeps the cache page's column layout and scroll across frames.
	CacheTable ui.ListState
	// CacheConfirmOpen asks before dropping cached rows; an empty
	// CacheConfirmPkg means the whole device.
	CacheConfirmOpen  bool
	CacheConfirmPkg   string
	CacheConfirmError string
	// DerpTable keeps the DERP list's column layout across frames.
	DerpTable components.TableState
	// DerpRegionTable keeps the detail page's region table layout.
	DerpRegionTable components.TableState
	// DerpEditor is the DERP editor's draft; nil closes the dialog.
	DerpEditor *DerpEditorDraft
	// DerpConfirmDelete asks before removing this preset; nil closes it.
	DerpConfirmDelete *core.RelayPreset
	// DerpDeleteError is why the delete was refused; shown in the dialog.
	DerpDeleteError string
	// DerpPreview is the map the detail page shows; nil while loading.
	DerpPreview *core.RelayMapPreview
	// DerpPreviewErr is why the last preview attempt failed.
	DerpPreviewErr string
	// DerpPreviewTried is the source a fetch was started for. Without it a
	// failed fetch would restart on every frame and its error would never stay
	// on screen long enough to click 重试.
	DerpPreviewTried string
	// DerpPreviewLoading is true while a preview fetch is in flight.
	DerpPreviewLoading bool
	// DerpJSONOpen is the detail page's raw JSON disclosure.
	DerpJSONOpen bool
	// derpPreviewMu guards what preview goroutines write.
	derpPreviewMu sync.Mutex
	// derpPreviews keeps the last preview of each source, so a frame never
	// re-parses a 128 KiB inline map.
	derpPreviews map[string]core.RelayMapPreview
	// derpInfo caches what the list shows about a source, for the same reason.
	derpInfo map[string]derpSourceInfo

	// PageLogFilter is the log page's filter; nil means it was never opened.
	PageLogFilter *logFilter
	// LogOutput is the task whose output dialog is open; nil closes it.
	LogOutput *logOutput
	// LogClear is the clear confirmation; nil closes it.
	LogClear *logClear
	// logMu guards what the log queries write from their goroutines.
	logMu sync.Mutex
	// activeLog is the log view drawing this frame; the previous one stops
	// following when the user navigates away.
	activeLog *logFilter

	// consoles holds the embedded terminal of each device.
	consoles map[uuid.UUID]*consoleHost
	// logFilters holds one log view per device, for the device page's tab.
	logFilters map[uuid.UUID]*logFilter
	// resources holds the resource page's state per device.
	resources map[uuid.UUID]*resourceState
	// appIcons caches decoded application icons, keyed by device and package,
	// together with the reading they were decoded from.
	appIcons map[string]iconAsset

	// files holds the file page's state per device; fileLoads and fileErrs are
	// keyed by "device|path" and filled from command goroutines, so they have
	// a lock of their own rather than riding the manager's.
	files     map[uuid.UUID]*fileState
	fileMu    sync.Mutex
	fileLoads map[string]bool
	fileErrs  map[string]string
}

// Fail reports an error as an error-level toast.
func (vm *ViewModel) Fail(err error) {
	if err == nil {
		return
	}
	vm.FailText(err.Error())
}

// FailText reports a message as an error-level toast, and keeps a redacted
// copy in the log: a failed action is exactly what a user comes to the log page
// looking for.
func (vm *ViewModel) FailText(text string) {
	if text == "" {
		return
	}
	vm.recordAppLog(core.LogError, text)
	vm.queueErrorToast(text)
}

// queueErrorToast reports a message as an error-level toast without logging it,
// for a message the caller already recorded at its own level.
func (vm *ViewModel) queueErrorToast(text string) {
	if text == "" {
		return
	}
	vm.queueToast(ui.Toast{
		Type:        components.ToastError,
		Title:       "操作失败",
		Description: text,
		Timeout:     8 * time.Second,
	})
}

// recordAppLog keeps one line for the log page. Success toasts are deliberately
// not recorded: "已复制" would bury the errors the page exists for.
func (vm *ViewModel) recordAppLog(level core.LogLevel, text string) {
	if vm.RecordAppLog == nil {
		return
	}
	// The address is the one thing that must never reach the log file; the
	// manager redacts again, so one missed caller is not a leak.
	vm.RecordAppLog(core.AppLogRecord{At: time.Now(), Level: level, Text: core.Redact(text, "")})
}

// Notify reports a completed action as a success toast.
func (vm *ViewModel) Notify(text string) {
	if text == "" {
		return
	}
	vm.queueToast(ui.Toast{Type: components.ToastSuccess, Title: text, Timeout: 3 * time.Second})
}

// queueToast stores a toast for the next frame and asks for that frame.
func (vm *ViewModel) queueToast(t ui.Toast) {
	vm.toastsMu.Lock()
	vm.toasts = append(vm.toasts, t)
	vm.toastsMu.Unlock()
	if vm.Invalidate != nil {
		// Window.Invalidate coalesces and posts itself: no round trip to the
		// main thread, which used to block command goroutines mid-frame.
		vm.Invalidate()
	}
}

// drainToasts takes what was queued since the last frame.
func (vm *ViewModel) drainToasts() []ui.Toast {
	vm.toastsMu.Lock()
	defer vm.toastsMu.Unlock()
	queued := vm.toasts
	vm.toasts = nil
	return queued
}

// EditorRequest asks the editor to open on a device, or on a new one.
type EditorRequest struct {
	// Profile is nil for a new device.
	Profile *core.Profile
	// Temporary marks a device that lives only in this run.
	Temporary bool
}

// newTemporaryDevice opens the editor on a fresh temporary device.
func (vm *ViewModel) newTemporaryDevice() {
	vm.PendingEditor = &EditorRequest{Temporary: true}
}

// NewViewModel wraps a manager.
func NewViewModel(m *core.Manager, locate core.LocateTool) *ViewModel {
	vm := &ViewModel{
		Manager:       m,
		Locate:        locate,
		Router:        ui.NewRouter(DeviceIndexRoute),
		CrumbChoice:   -1,
		SavedOpen:     true,
		TemporaryOpen: true,
	}
	if profiles := m.AllProfiles(); len(profiles) > 0 {
		vm.Selection = profiles[0].ID
	}
	return vm
}

// DeviceIndexRoute is the page that lists the devices.
const DeviceIndexRoute = "/devices"

// LogsRoute is the page that merges the app's own lines, every channel and
// every task into one searchable timeline.
const LogsRoute = "/logs"

// CacheRoute is the management page for cached application names and icons.
const CacheRoute = "/manage/appmeta"

// DerpRoute lists every DERP map source; DerpDetailRoute shows one preset.
const DerpRoute = "/manage/derp"

// DerpDetailRoute is the page of one saved DERP preset.
func DerpDetailRoute(id uuid.UUID) string { return DerpRoute + "/" + id.String() }

// DeviceRoute is the page of one device.
func DeviceRoute(id uuid.UUID) string { return DeviceIndexRoute + "/" + id.String() }

// Select shows one device without navigating, for tests and startup.
func (vm *ViewModel) Select(id uuid.UUID) { vm.Selection = id }

// OpenDevice shows the page of one device.
func (vm *ViewModel) OpenDevice(id uuid.UUID) {
	vm.Selection = id
	if vm.Router == nil {
		return
	}
	if target := DeviceRoute(id); vm.Router.Path() != target {
		vm.Router.Push(target)
	}
}

// OpenCache shows the application metadata cache page.
func (vm *ViewModel) OpenCache() {
	if vm.Router != nil && vm.Router.Path() != CacheRoute {
		vm.Router.Push(CacheRoute)
	}
}

// OpenDerp shows the DERP list.
func (vm *ViewModel) OpenDerp() {
	if vm.Router != nil && vm.Router.Path() != DerpRoute {
		vm.Router.Push(DerpRoute)
	}
}

// OpenDerpDetail shows one preset.
func (vm *ViewModel) OpenDerpDetail(id uuid.UUID) {
	if vm.Router == nil {
		return
	}
	if target := DerpDetailRoute(id); vm.Router.Path() != target {
		vm.Router.Push(target)
	}
}

// OpenLogs shows the log page.
func (vm *ViewModel) OpenLogs() {
	if vm.Router != nil && vm.Router.Path() != LogsRoute {
		vm.Router.Push(LogsRoute)
	}
}

// OpenDevices goes back to the device index.
func (vm *ViewModel) OpenDevices() {
	if vm.Router != nil && vm.Router.Path() != DeviceIndexRoute {
		vm.Router.Push(DeviceIndexRoute)
	}
}

// profileByID finds a saved or temporary device by its route parameter.
func (vm *ViewModel) profileByID(raw string) (core.Profile, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return core.Profile{}, false
	}
	for _, profile := range vm.Manager.AllProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return core.Profile{}, false
}

// deviceIDFromPath returns the device a route path names. It is the routing
// the title bar needs without entering Router.View.
func deviceIDFromPath(path string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(path, DeviceIndexRoute+"/")
	if !ok || strings.Contains(rest, "/") {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// ContentTitle fills the shell header's leading slot: the path back to the
// device index on a device page, and the page name everywhere else. It lives
// in the title bar so the header's drag strip can never swallow the click.
func (vm *ViewModel) ContentTitle(c *ui.Context) {
	pageName := "我的设备"
	if vm.Router == nil {
		components.Text(c, pageName).Bold().SingleLine()
		return
	}
	id, ok := deviceIDFromPath(vm.Router.Path())
	if !ok {
		if trail, isManage := vm.managePageTrail(vm.Router.Path()); isManage {
			vm.CrumbChoice = -1
			crumbs := components.Breadcrumbs(c, append([]string{pageName}, trail...), &vm.CrumbChoice)
			if crumbs.Changed() {
				switch {
				case vm.CrumbChoice == 0:
					vm.OpenDevices()
				case vm.CrumbChoice == 1 && len(trail) > 1:
					// The intermediate crumb of a detail page is its list.
					vm.OpenDerp()
				}
			}
			return
		}
		components.Text(c, pageName).Bold().SingleLine()
		return
	}
	profile, found := vm.profileByID(id.String())
	if !found {
		components.Text(c, "设备不存在").Bold().SingleLine()
		return
	}
	vm.CrumbChoice = -1
	crumbs := components.Breadcrumbs(c, []string{pageName, profile.Name}, &vm.CrumbChoice)
	if crumbs.Changed() && vm.CrumbChoice == 0 {
		vm.OpenDevices()
	}
}

// SelectedProfile returns the device the detail pane shows.
func (vm *ViewModel) SelectedProfile() (core.Profile, bool) {
	for _, p := range vm.Manager.AllProfiles() {
		if p.ID == vm.Selection {
			return p, true
		}
	}
	return core.Profile{}, false
}

func (vm *ViewModel) ensureSelection() {
	if _, ok := vm.SelectedProfile(); ok {
		return
	}
	vm.Selection = uuid.Nil
	if profiles := vm.Manager.AllProfiles(); len(profiles) > 0 {
		vm.Selection = profiles[0].ID
	}
}

// deviceRow is the sidebar line for one device.
func (vm *ViewModel) deviceRow(c *ui.Context, profile core.Profile, temporary bool) {
	theme := c.Theme()
	selected := vm.Selection == profile.ID
	session := vm.Manager.Session(profile.ID)

	// Fill() would stretch the row to the full column height; a row only
	// needs to span the width. ButtonBase centers its content, so the row
	// has to be told to start at the left edge.
	row := ui.ButtonBase(c)
	row.FillWidth().Shrink(0).Padding(theme.Space(1), theme.Space(1.5)).Gap(theme.Space(1.5)).
		Justify(ui.Start).AlignItems(ui.Center).Radius(theme.Radius)
	if selected {
		row.Background(selectedRowTint(theme))
	}
	line := session.State.Label()
	switch {
	case profile.DirectADB:
		line += " · " + orPlaceholder(profile.DirectTarget, "未填地址")
	case session.Port != 0:
		line += " · " + itoa(session.Port)
	}
	row.Children(func() {
		statusDot(c, session.State)
		ui.Column(c).Gap(1).Children(func() {
			// The sidebar is narrow: a long name or a port line gets clipped,
			// so each one carries its full text as a tooltip.
			components.Tooltip(c, components.Text(c, profile.Name).Bold().SingleLine(), profile.Name)
			components.Tooltip(c, components.Text(c, line).FontSize(11).SingleLine(), line)
		})
		if temporary {
			components.Icon(c, lucide.Zap).Width(12).Height(12)
		}
	})
	if row.Clicked() {
		vm.OpenDevice(profile.ID)
	}
}

// statusDot draws the state indicator.
func statusDot(c *ui.Context, state core.ConnectionState) {
	theme := c.Theme()
	color := theme.TextMuted
	switch state {
	case core.StateOnline:
		color = theme.Success
	case core.StateUnauthorized, core.StateOffline, core.StateNeedsAction:
		color = theme.Warning
	case core.StateFailed:
		color = theme.Danger
	case core.StateConnecting, core.StateDisconnecting:
		color = theme.Accent
	}
	ui.Column(c).Width(8).Height(8).Radius(4).Background(color)
}

// actionButton is a labelled button with a leading icon. Window-level actions
// use it, so a row of them reads as actions rather than as body text.
func actionButton(c *ui.Context, icon func() *ui.SVG, label string, variant components.ButtonVariant) ui.Element {
	theme := c.Theme()
	size := max(float32(12), theme.Rem(.875))
	b := components.Button(c, "", variant).Label(label)
	b.Gap(theme.Space(1)).Children(func() {
		components.Icon(c, icon).Size(size, size)
		components.Text(c, label).SingleLine()
	})
	return b
}

// SidebarView is the scrolling device list. The floating window header sits
// above it, so it starts below HeaderHeight.
func (vm *ViewModel) SidebarView(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), theme.Space(2), 0, theme.Space(2)).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			// The gap keeps the browsing group apart from the device panels:
			// without it the entry reads as one more device row.
			ui.Column(c).FillWidth().Gap(theme.Space(2)).Children(func() {
				vm.deviceGroups(c)
				// Pages, not window actions: they belong under the device list,
				// inside the scrolling area, rather than in the fixed footer.
				ui.Column(c).FillWidth().Padding(0, 4).Gap(2).Children(func() { vm.manageEntries(c) })
			})
		})
	})
}

// deviceGroups renders both groups as accordion sections.
func (vm *ViewModel) deviceGroups(c *ui.Context) {
	components.Accordion(c, func(group *components.AccordionGroup) {
		group.Item("我的设备 "+countLabel(len(vm.Manager.Profiles())), &vm.SavedOpen, func() {
			vm.deviceGroupRows(c, vm.Manager.Profiles(), false, "还没有设备，用工具栏的“添加设备”创建一台。")
		})
		group.Item("临时设备 "+countLabel(len(vm.Manager.TemporaryProfiles())), &vm.TemporaryOpen, func() {
			vm.deviceGroupRows(c, vm.Manager.TemporaryProfiles(), true, "临时设备只在本次运行有效。")
		})
	})
}

// deviceGroupRows lists one group, or an explanation when it is empty.
func (vm *ViewModel) deviceGroupRows(c *ui.Context, profiles []core.Profile, temporary bool, empty string) {
	if len(profiles) == 0 {
		components.Text(c, empty).FontSize(11).Padding(2, 4)
		return
	}
	for _, profile := range profiles {
		vm.deviceRow(c, profile, temporary)
	}
}

// countLabel renders the "(n)" suffix of a section title, so a collapsed
// group still says how many devices it holds.
func countLabel(n int) string {
	if n == 0 {
		return "(空)"
	}
	return "(" + itoa(n) + ")"
}

// SidebarFooter is the fixed bottom toolbar: channel count, tool health and
// the temporary-connection action.
func (vm *ViewModel) SidebarFooter(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Padding(theme.Space(2), theme.Space(2.5)).Gap(theme.Space(1.5)).Children(func() {
		for _, component := range core.AllTools() {
			vm.toolRow(c, component)
		}
		if actionButton(c, lucide.Settings, "工具设置", components.ButtonSecondary).Clicked() {
			vm.openSettings()
		}
	})
}

// toolRow says whether a tool is usable, and offers to install it when not.
func (vm *ViewModel) toolRow(c *ui.Context, component core.ToolComponent) {
	path := vm.Manager.Tools().FindToolPath(component, vm.Locate)
	ui.Row(c).FillWidth().Gap(6).Children(func() {
		if path != "" {
			components.Icon(c, lucide.CircleCheck).Width(12).Height(12)
			components.Text(c, component.Label()).FontSize(11)
			components.Text(c, "可用").FontSize(11)
			return
		}
		components.Icon(c, lucide.CircleAlert).Width(12).Height(12)
		components.Text(c, component.Label()).FontSize(11)
		if components.Button(c, "安装", components.ButtonOutline).Height(22).FontSize(11).Clicked() && vm.Install != nil {
			vm.Install(component)
		}
	})
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
