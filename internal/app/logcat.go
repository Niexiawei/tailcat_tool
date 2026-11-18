package app

import (
	"context"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// logcatState is the one streaming log view: what the user chose, what the
// stream kept, and the table state that holds the column layout and the scroll.
//
// Every field is written on the render path only: the stream asks for a frame
// from its own goroutine and nothing else crosses over, so the page needs no
// lock of its own. The stream has one.
type logcatState struct {
	Device *uuid.UUID
	Filter core.LogcatFilter
	// Follow is the 跟随最新 switch; ListState.FollowEnd mirrors it.
	Follow bool
	Paused bool
	// stream is the running logcat, nil when there is none.
	stream *core.LogcatStream
	rows   []core.LogcatEntry
	// version and filterKey are what the rows were read from: a frame that
	// finds both unchanged does not copy the buffer again.
	version   uint64
	filterKey string
	received  uint64
	dropped   uint64
	exited    bool
	err       string
}

// logcatState returns the page's state, creating it on first use. It lives on
// the view model, so leaving the page and coming back keeps the filters.
func (vm *ViewModel) logcatState() *logcatState {
	if vm.Logcat == nil {
		vm.Logcat = &logcatState{Follow: true}
	}
	return vm.Logcat
}

// onlineDevices lists the devices whose ADB session is up. A log stream needs a
// live serial, so a device that is merely saved is not on this list.
func (vm *ViewModel) onlineDevices() []core.Profile {
	var online []core.Profile
	for _, profile := range vm.Manager.AllProfiles() {
		session := vm.Manager.Session(profile.ID)
		if session.State == core.StateOnline && session.Serial != "" {
			online = append(online, profile)
		}
	}
	return online
}

// beginLogcatFrame marks the streaming page as not drawn yet; logcatPage sets it
// back while it builds, so a frame that did not draw it closes the stream.
func (vm *ViewModel) beginLogcatFrame() { vm.logcatDrawn = false }

// settleLogcat stops the stream when its page was not drawn this frame: a
// hidden page must not keep pulling lines off the device.
func (vm *ViewModel) settleLogcat() {
	if vm.logcatDrawn {
		return
	}
	if vm.Logcat != nil {
		vm.stopLogcatStream(vm.Logcat)
	}
}

// stopLogcatStream closes whatever is running for the page.
func (vm *ViewModel) stopLogcatStream(f *logcatState) {
	stream := f.stream
	if stream == nil {
		return
	}
	f.stream = nil
	if f.Device != nil {
		vm.Manager.StopLogcat(*f.Device)
		return
	}
	stream.Close()
}

// logcatPage is the Logcat page: the device, then the filters, then the stream.
func (vm *ViewModel) logcatPage(c *ui.Context) {
	vm.logcatDrawn = true
	f := vm.logcatState()
	vm.ensureLogcatDevice(f)
	vm.ensureLogcatStream(f)
	vm.refreshLogcatRows(f)
	theme := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(theme.Space(2)).Children(func() {
		online := vm.onlineDevices()
		if len(online) == 0 {
			placeholder(c, "没有已连接的设备，先到设备页连接一台。", 16)
			return
		}
		vm.logcatDeviceSelect(c, f, online)
		vm.logcatStatus(c, f)
	})
}

// ensureLogcatDevice keeps the chosen device honest: a device that went away is
// dropped, and an empty choice falls back to the first connected one.
func (vm *ViewModel) ensureLogcatDevice(f *logcatState) {
	online := vm.onlineDevices()
	if f.Device != nil {
		for _, profile := range online {
			if profile.ID == *f.Device {
				return
			}
		}
	}
	if len(online) == 0 {
		f.Device = nil
		return
	}
	id := online[0].ID
	f.Device = &id
}

// ensureLogcatStream keeps one stream running for the chosen device.
func (vm *ViewModel) ensureLogcatStream(f *logcatState) {
	if f.Device == nil {
		vm.stopLogcatStream(f)
		return
	}
	if f.stream != nil && !f.stream.Closed() {
		return
	}
	f.stream = nil
	stream, err := vm.Manager.StartLogcat(context.Background(), *f.Device, core.LogcatOptions{OnLines: vm.requestFrame})
	if err != nil {
		f.err = err.Error()
		return
	}
	f.err = ""
	f.stream = stream
}

// refreshLogcatRows copies what the stream holds into the page's state.
func (vm *ViewModel) refreshLogcatRows(f *logcatState) {
	if f.stream == nil {
		return
	}
	filterKey := string(f.Filter.MinLevel) + "\x00" + f.Filter.Tags + "\x00" + f.Filter.Terms
	if f.Paused && f.version != 0 && f.filterKey == filterKey {
		return
	}
	if f.stream.Version() == f.version && f.filterKey == filterKey && f.version != 0 {
		return
	}
	snap := f.stream.Snapshot(f.Filter)
	f.rows = snap.Entries
	f.version = snap.Version
	f.filterKey = filterKey
	f.received = snap.Received
	f.dropped = snap.Dropped
	f.exited = snap.Exited
	f.err = snap.Err
}

// logcatDeviceSelect chooses which connected device to stream.
func (vm *ViewModel) logcatDeviceSelect(c *ui.Context, f *logcatState, online []core.Profile) {
	choice := ""
	if f.Device != nil {
		choice = f.Device.String()
	}
	sel := components.Select(c, &choice)
	sel.Trigger.FillWidth()
	sel.Trigger.Children(func() {
		components.Text(c, vm.logcatDeviceLabel(choice, online)).SingleLine()
	})
	sel.Popup(func(ui.Element) {
		for _, profile := range online {
			item := sel.Item(profile.ID.String())
			item.Children(func() { components.Text(c, profile.Name+" · "+vm.Manager.Session(profile.ID).Serial).SingleLine() })
		}
	})
	if sel.Trigger.Changed() {
		if id, err := uuid.Parse(choice); err == nil && (f.Device == nil || *f.Device != id) {
			vm.stopLogcatStream(f)
			f.Device = &id
			f.rows = nil
			f.version = 0
			f.filterKey = ""
			f.err = ""
			f.exited = false
			f.received, f.dropped = 0, 0
		}
	}
}

// logcatDeviceLabel is what the picker's trigger shows.
func (vm *ViewModel) logcatDeviceLabel(choice string, online []core.Profile) string {
	id, err := uuid.Parse(choice)
	if err != nil {
		return "选择设备"
	}
	for _, profile := range online {
		if profile.ID == id {
			return profile.Name + " · " + vm.Manager.Session(id).Serial
		}
	}
	return "选择设备"
}

// logcatStatus is the line under the picker: what the stream is doing.
func (vm *ViewModel) logcatStatus(c *ui.Context, f *logcatState) {
	theme := c.Theme()
	switch {
	case f.err != "" && f.exited:
		components.Text(c, "日志流已结束："+f.err).FontSize(13).TextColor(theme.Warning)
	case f.exited:
		components.Text(c, "日志流已结束").FontSize(13).TextColor(theme.TextMuted)
	case f.err != "":
		components.Text(c, "日志流无法启动："+f.err).FontSize(13).TextColor(theme.Danger)
	default:
		components.Text(c, "正在接收日志…").FontSize(13).TextColor(theme.TextMuted)
	}
}

// placeholder centres a message in the box the page keeps for its content.
func placeholder(c *ui.Context, text string, size float32) {
	ui.Column(c).Fill().Center().Children(func() {
		components.Text(c, text).FontSize(size).TextColor(c.Theme().TextMuted)
	})
}
