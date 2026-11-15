package app

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// AttentionDialog asks the user what to do when something outside the app
// changed a channel. It only appears while the window is being drawn, which is
// the moment the user is looking at it.
func (vm *ViewModel) AttentionDialog(c *ui.Context) {
	if vm.AttentionOpen || len(vm.Manager.Attentions()) == 0 {
		return
	}
	vm.AttentionOpen = true
	open := true
	components.Dialog(c, &open, components.DialogOptions{
		Label:                "设备需要处理",
		DisableBackdropClose: true,
		DisableEscapeClose:   true,
	}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(470)
		panel.Children(func() {
			attentions := vm.Manager.Attentions()
			if len(attentions) == 0 {
				vm.AttentionOpen = false
				open = false
				return
			}
			attention := attentions[0]
			name := ""
			for _, profile := range vm.Manager.AllProfiles() {
				if profile.ID == attention.DeviceID {
					name = profile.Name
				}
			}
			components.Text(c, "设备需要处理").FontSize(18).Bold()
			components.Text(c, name).FontSize(12)
			components.Text(c, attention.Detail).FontSize(11)
			ui.Row(c).Fill().Gap(8).Children(func() {
				if components.Button(c, "重新连接", components.ButtonPrimary).Clicked() {
					deviceID := attention.DeviceID
					vm.Manager.AckAttention(deviceID)
					vm.AttentionOpen = false
					open = false
					go func() { _ = vm.Manager.Connect(context.Background(), deviceID) }()
				}
				if components.Button(c, "改用其他本地端口", components.ButtonSecondary).Clicked() {
					deviceID := attention.DeviceID
					vm.Manager.AckAttention(deviceID)
					vm.AttentionOpen = false
					open = false
					go func() { _ = vm.Manager.SwitchPort(context.Background(), deviceID, 0, true) }()
				}
				if components.Button(c, "稍后处理", components.ButtonGhost).Clicked() {
					vm.Manager.AckAttention(attention.DeviceID)
					vm.AttentionOpen = false
					open = false
					vm.Notice = "该设备保持“等待处理”，可随时重新打开提示"
				}
			})
		})
	})
	if !open {
		vm.AttentionOpen = false
	}
}
