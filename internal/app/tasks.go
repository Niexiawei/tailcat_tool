package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
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
	ui.Column(c).FillWidth().Shrink(0).Padding(8, 20).Gap(4).Children(func() {
		components.Text(c, "设备任务").FontSize(13).Bold()
		ui.Scroll(c).FillWidth().Height(150).Children(func() {
			for i := len(tasks) - 1; i >= start; i-- {
				task := tasks[i]
				vm.taskRow(c, task)
			}
		})
	})
}

// taskRow is one line of the task panel.
func (vm *ViewModel) taskRow(c *ui.Context, task core.Task) {
	ui.Row(c).FillWidth().Shrink(0).Gap(8).Children(func() {
		ui.Column(c).Gap(1).Children(func() {
			components.Text(c, task.Title).FontSize(12).SingleLine()
			components.Text(c, task.DeviceName+" · "+task.Serial).FontSize(10).SingleLine()
		})
		components.Text(c, task.State.Label()).FontSize(11)
		if task.State == core.TaskRunning {
			if components.Button(c, "取消", components.ButtonGhost).Clicked() {
				vm.Manager.CancelTask(task.ID)
			}
		}
	})
}
