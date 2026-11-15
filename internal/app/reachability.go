package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// openReachability starts a batch probe and shows the table.
func (vm *ViewModel) openReachability() {
	vm.ReachabilityOpen = true
	vm.Manager.TestAllDevices()
}

// ReachabilityView is the batch reachability table.
func (vm *ViewModel) ReachabilityView(c *ui.Context) {
	open := vm.ReachabilityOpen
	components.Dialog(c, &open, components.DialogOptions{Label: "全部设备连通测试"}, func(panel ui.Element) {
		panel.Padding(18).Gap(8).Width(640)
		panel.Children(func() {
			components.Text(c, "全部设备连通测试").FontSize(18).Bold()
			components.Text(c, "测试全部设备的 Tailcat ping，每次探测最长 8 秒，同时测试 3 台；不会建立或断开 ADB 连接。").FontSize(11)
			results := vm.Manager.Reachability().SortedResults()
			ui.Row(c).Fill().Gap(8).Children(func() {
				components.Text(c, "设备").FontSize(11)
				components.Text(c, "Tailcat ping").FontSize(11)
				components.Text(c, "延迟").FontSize(11)
				components.Text(c, "路径 / 详情").FontSize(11)
			})
			for _, result := range results {
				result := result
				detail := result.Route
				if detail == "" || detail == "—" {
					detail = result.Detail
				}
				ui.Row(c).Fill().Gap(8).Children(func() {
					components.Text(c, result.Name).FontSize(11)
					components.Text(c, result.State.Label()).FontSize(11)
					components.Text(c, result.Latency).FontSize(11)
					components.Text(c, detail).FontSize(11)
				})
			}
			ui.Row(c).Fill().Gap(8).Children(func() {
				if components.Button(c, "重新测试", components.ButtonSecondary).Clicked() {
					vm.Manager.TestAllDevices()
				}
				if components.Button(c, "取消测试", components.ButtonGhost).Clicked() {
					vm.Manager.Reachability().Cancel()
				}
				if components.Button(c, "完成", components.ButtonPrimary).Clicked() {
					vm.ReachabilityOpen = false
					open = false
				}
			})
		})
	})
	if !open {
		vm.ReachabilityOpen = false
	}
}
