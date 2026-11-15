package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// openSettings copies the manager's tool settings into the form.
func (vm *ViewModel) openSettings() {
	vm.SettingsOpen = true
	vm.SettingsError = ""
	vm.SettingsDraft = vm.Manager.Tools()
}

// SettingsView is the tool settings form.
func (vm *ViewModel) SettingsView(c *ui.Context) {
	open := vm.SettingsOpen
	components.Dialog(c, &open, components.DialogOptions{Label: "工具设置"}, func(panel ui.Element) {
		panel.Padding(18).Gap(10).Width(520)
		panel.Children(func() {
			components.Text(c, "工具设置").FontSize(18).Bold()
			field(c, "ADB", &vm.SettingsDraft.ADB, false)
			field(c, "scrcpy（可选）", &vm.SettingsDraft.Scrcpy, false)
			for _, component := range core.AllTools() {
				path := vm.SettingsDraft.FindToolPath(component, vm.Locate)
				if path == "" {
					components.Text(c, component.Label()+" 未找到，可点“安装缺失组件”").FontSize(11)
				}
			}
			if vm.SettingsError != "" {
				components.Text(c, vm.SettingsError).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "关闭", components.ButtonGhost).Clicked() {
					vm.SettingsOpen = false
					open = false
				}
				if components.Button(c, "安装缺失组件", components.ButtonSecondary).Clicked() && vm.Install != nil {
					vm.Install(core.ToolADB)
				}
				if components.Button(c, "保存", components.ButtonPrimary).Clicked() {
					if err := vm.Manager.UpdateTools(vm.SettingsDraft); err != nil {
						vm.SettingsError = err.Error()
					} else {
						vm.SettingsOpen = false
						vm.Notice = "已保存工具设置"
					}
				}
			})
		})
	})
	if !open {
		vm.SettingsOpen = false
	}
}
