// Package app builds the Tailcat Manager window on mygo and onevra.
package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// ViewModel is everything the window reads. It owns no manager state of its
// own: the manager is the single source of truth and the window redraws when
// it says something changed.
type ViewModel struct {
	Manager      *core.Manager
	Locate       core.LocateTool
	Install      func(core.ToolComponent)
	Activate     func(deviceID string)
	OpenTerminal func(deviceID uuid.UUID) error

	// Selection is the device the detail pane shows.
	Selection uuid.UUID
	// DetailTab is 0 for the overview and 1 for the log.
	DetailTab int
	// Demo marks the simulated environment.
	Demo bool
	// Notice is the one-line banner; empty hides it.
	Notice string
	// PendingEditor is the device form that is open, nil when closed.
	PendingEditor *EditorRequest

	// Form state. MyGo views are functions of state, so every text field is a
	// field here.
	EditorDraft      EditorInput
	EditorError      string
	EditorPresetName string

	TransferOpen       bool
	TransferUpload     bool
	TransferLocalPath  string
	TransferRemotePath string
	TransferError      string

	SettingsOpen  bool
	SettingsDraft core.ToolSettings
	SettingsError string

	ReachabilityOpen bool
	ShellCommand     string
	AttentionOpen    bool
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
	vm := &ViewModel{Manager: m, Locate: locate}
	if profiles := m.AllProfiles(); len(profiles) > 0 {
		vm.Selection = profiles[0].ID
	}
	return vm
}

// Select shows one device.
func (vm *ViewModel) Select(id uuid.UUID) { vm.Selection = id }

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

	row := ui.ButtonBase(c)
	row.Fill().Padding(theme.Space(1), theme.Space(1.5)).Gap(theme.Space(1.5))
	if selected {
		row.Background(theme.Selection)
	}
	row.Children(func() {
		statusDot(c, session.State)
		ui.Column(c).Gap(1).Children(func() {
			components.Text(c, profile.Name).Bold().SingleLine()
			line := session.State.Label()
			if session.Port != 0 {
				line += " · " + itoa(session.Port)
			}
			components.Text(c, line).FontSize(11)
		})
		if temporary {
			components.Icon(c, lucide.Zap).Width(12).Height(12)
		}
	})
	if row.Clicked() {
		vm.Select(profile.ID)
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

// SidebarView is the device list plus the tool health footer.
func (vm *ViewModel) SidebarView(c *ui.Context) {
	vm.ensureSelection()
	saved := vm.Manager.Profiles()
	temporary := vm.Manager.TemporaryProfiles()

	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Fill().Padding(10, 8).Gap(2).Children(func() {
			sectionHeader(c, "我的设备")
			for _, profile := range saved {
				vm.deviceRow(c, profile, false)
			}
			sectionHeader(c, "临时设备")
			for _, profile := range temporary {
				vm.deviceRow(c, profile, true)
			}
			if components.Button(c, "新建临时连接", components.ButtonGhost).Clicked() {
				vm.newTemporaryDevice()
			}
		})
		vm.footer(c)
	})
}

func sectionHeader(c *ui.Context, text string) {
	components.Text(c, text).FontSize(11).Bold().Padding(6, 8, 2, 8)
}

// footer shows the channel counts and the state of both tools.
func (vm *ViewModel) footer(c *ui.Context) {
	ui.Column(c).Padding(10, 8).Gap(4).Children(func() {
		components.Text(c, "GUI "+itoa(vm.Manager.OwnedActiveCount())).FontSize(11)
		for _, component := range core.AllTools() {
			vm.toolRow(c, component)
		}
		if notice := vm.Notice; notice != "" {
			components.Text(c, notice).FontSize(11)
		}
	})
}

// toolRow says whether a tool is usable, and offers to install it when not.
func (vm *ViewModel) toolRow(c *ui.Context, component core.ToolComponent) {
	path := vm.Manager.Tools().FindToolPath(component, vm.Locate)
	ui.Row(c).Fill().Gap(6).Children(func() {
		if path != "" {
			components.Icon(c, lucide.CircleCheck).Width(12).Height(12)
			components.Text(c, component.Label()).FontSize(11)
			components.Text(c, "可用").FontSize(11)
			return
		}
		components.Icon(c, lucide.CircleAlert).Width(12).Height(12)
		components.Text(c, component.Label()).FontSize(11)
		if components.Button(c, "安装", components.ButtonGhost).Clicked() && vm.Install != nil {
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
