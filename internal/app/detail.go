package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// ContentView is the detail pane for the selected device.
func (vm *ViewModel) ContentView(c *ui.Context) {
	profile, ok := vm.SelectedProfile()
	if !ok {
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			components.Text(c, "连接你的第一台设备").FontSize(20).Bold()
			components.Text(c, "每台设备拥有独立通道，支持同时连接。").FontSize(12)
		})
		return
	}
	session := vm.Manager.Session(profile.ID)
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		components.Text(c, profile.Name).FontSize(20).Bold()
		components.Text(c, session.State.Label()).FontSize(12)
		if session.Err != "" {
			components.Text(c, session.Err).FontSize(12)
		}
	})
}
