package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// settingsPage is the tool settings form as a page. It is where the sidebar's
// health rows get fixed: the footer says whether adb and scrcpy resolve, this
// page says to what and lets the user correct it.
func (vm *ViewModel) settingsPage(c *ui.Context) {
	theme := c.Theme()
	if !vm.SettingsLoaded {
		vm.SettingsDraft = vm.Manager.Tools()
		vm.SettingsError = ""
		vm.SettingsLoaded = true
	}
	saved := vm.Manager.Tools()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(20).Gap(12).Children(func() {
			components.Text(c, "工具设置").FontSize(18).Bold()
			components.Text(c, "ADB 是必需的，scrcpy 用于投屏。留空表示按 PATH 自动查找；改动在重新连接设备后生效。").
				FontSize(11).TextColor(theme.TextMuted)

			field(c, "ADB（必需）", &vm.SettingsDraft.ADB, false)
			field(c, "scrcpy（可选）", &vm.SettingsDraft.Scrcpy, false)

			// What each tool currently resolves to, computed from the draft so
			// a corrected path is confirmed before it is saved.
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				for _, component := range core.AllTools() {
					vm.toolStatusRow(c, component)
				}
			})

			if vm.SettingsError != "" {
				components.Text(c, vm.SettingsError).FontSize(11).TextColor(theme.Warning)
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if components.Button(c, "保存", components.ButtonPrimary).Clicked() {
					if err := vm.Manager.UpdateTools(vm.SettingsDraft); err != nil {
						vm.SettingsError = err.Error()
					} else {
						vm.SettingsError = ""
						vm.Notify("已保存工具设置")
					}
				}
				if components.Button(c, "放弃修改", components.ButtonOutline).Disabled(vm.SettingsDraft == saved).Clicked() {
					vm.SettingsDraft = saved
					vm.SettingsError = ""
				}
			})
		})
	})
}

// toolStatusRow answers "where is it, and if not, install it" for one tool, for
// the path currently in the draft rather than the saved one.
func (vm *ViewModel) toolStatusRow(c *ui.Context, component core.ToolComponent) {
	theme := c.Theme()
	path := vm.SettingsDraft.FindToolPath(component, vm.Locate)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		if path == "" {
			components.Icon(c, lucide.CircleAlert).Width(12).Height(12)
			components.Text(c, component.Label()).FontSize(11).SingleLine().TextColor(theme.Warning)
			components.Text(c, "未找到").FontSize(11).TextColor(theme.Warning)
			components.Text(c, "可安装，或在上面的框里填绝对路径").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			if components.Button(c, "安装", components.ButtonOutline).Height(22).FontSize(11).Clicked() && vm.Install != nil {
				vm.Install(component)
			}
			return
		}
		components.Icon(c, lucide.CircleCheck).Width(12).Height(12)
		components.Text(c, component.Label()).FontSize(11).SingleLine()
		// A path can be longer than the page is wide, and it is the one thing
		// that says which binary will run, so it keeps a full-text tooltip.
		components.Tooltip(c, components.Text(c, path).FontSize(11).TextColor(theme.TextMuted).SingleLine(), path).Grow(1)
	})
}
