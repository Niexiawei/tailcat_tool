package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// AttentionDialog asks the user what to do when something outside the app
// changed a channel. It only appears while the window is being drawn, which
// is the moment the user is looking at it.
//
// One prompt can cover several devices: "adb kill-server" drops every local
// entry at once, and asking per device would read as a series of user mistakes.
// It is built on every frame while an attention exists: MyGo views are
// functions of state, so a dialog drawn only on the frame that detected the
// event is a dialog that vanishes before it can be clicked.
func (vm *ViewModel) AttentionDialog(c *ui.Context) {
	if len(vm.Manager.Attentions()) == 0 {
		// Nothing is waiting: the next frame simply leaves the dialog out.
		vm.AttentionOpen = false
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
			devices := attention.Devices()
			names := vm.deviceNames(devices)
			components.Text(c, "设备需要处理").FontSize(18).Bold()
			components.Text(c, strings.Join(names, "、")).FontSize(12)
			components.Text(c, attention.Detail).FontSize(11)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if len(devices) > 1 {
					// The whole fleet came back at once; bringing it back is one
					// action, and choosing a port for one of them is not the
					// question the user is being asked.
					if components.Button(c, "全部重新连接", components.ButtonPrimary).Clicked() {
						vm.Manager.AckAttention(attention.DeviceID)
						vm.AttentionOpen = false
						open = false
						fleet := devices
						go func() {
							if err := vm.Manager.ReconnectAll(context.Background(), fleet); err != nil {
								vm.Fail(err)
							}
						}()
					}
				} else if components.Button(c, "重新连接", components.ButtonPrimary).Clicked() {
					deviceID := attention.DeviceID
					vm.Manager.AckAttention(deviceID)
					vm.AttentionOpen = false
					open = false
					go func() { _ = vm.Manager.Connect(context.Background(), deviceID) }()
				}
				if len(devices) == 1 {
					if components.Button(c, "改用其他本地端口", components.ButtonSecondary).Clicked() {
						deviceID := attention.DeviceID
						vm.Manager.AckAttention(deviceID)
						vm.AttentionOpen = false
						open = false
						go func() { _ = vm.Manager.SwitchPort(context.Background(), deviceID, 0, true) }()
					}
				}
				if components.Button(c, "稍后处理", components.ButtonOutline).Clicked() {
					vm.Manager.AckAttention(attention.DeviceID)
					vm.AttentionOpen = false
					open = false
					if len(devices) > 1 {
						vm.Notify("这 " + strconv.Itoa(len(devices)) + " 台设备保持“等待处理”，可随时重新打开提示")
					} else {
						vm.Notify("该设备保持“等待处理”，可随时重新打开提示")
					}
				}
			})
		})
	})
	if !open {
		vm.AttentionOpen = false
	}
}

// deviceNames names the devices an attention covers, in the order the manager
// reported them. A device removed between the detection and this frame is left
// out rather than shown as a bare id.
func (vm *ViewModel) deviceNames(ids []uuid.UUID) []string {
	byID := map[uuid.UUID]string{}
	for _, profile := range vm.Manager.AllProfiles() {
		byID[profile.ID] = profile.Name
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := byID[id]; ok {
			names = append(names, name)
		}
	}
	return names
}
