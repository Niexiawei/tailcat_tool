package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// TaskPanel lists the most recent tasks of every device.
func (vm *ViewModel) TaskPanel(c *ui.Context) {
	tasks := vm.Manager.Tasks()
	if len(tasks) == 0 {
		return
	}
	start := 0
	if len(tasks) > 20 {
		start = len(tasks) - 20
	}
	ui.Column(c).Fill().Padding(0, 20).Gap(4).Children(func() {
		components.Text(c, "设备任务").FontSize(13).Bold()
		for i := len(tasks) - 1; i >= start; i-- {
			task := tasks[i]
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Column(c).Gap(1).Children(func() {
					components.Text(c, task.Title).FontSize(12)
					components.Text(c, task.DeviceName+" · "+task.Serial).FontSize(10)
				})
				components.Text(c, task.State.Label()).FontSize(11)
				if task.State == "running" {
					if components.Button(c, "取消", components.ButtonGhost).Clicked() {
						vm.Manager.CancelTask(task.ID)
					}
				}
			})
		}
	})
}
