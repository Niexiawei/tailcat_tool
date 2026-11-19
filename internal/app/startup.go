package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// startupPage is what the window shows when a piece of the app would not load.
// It replaces the routed content on purpose: a device list that looks fine
// while the history database is gone teaches the user nothing.
func (vm *ViewModel) startupPage(c *ui.Context) {
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(760).Padding(theme.Space(3)).Gap(theme.Space(2)).Children(func() {
			ui.Row(c).FillWidth().Gap(theme.Space(1.5)).AlignItems(ui.Center).Children(func() {
				components.Icon(c, lucide.TriangleAlert()).Size(20, 20).TextColor(theme.Warning)
				components.Text(c, "启动时发现问题").FontSize(18).Bold()
			})
			components.Text(c, "应用仍然可以使用，下面这些能力本次运行会打折扣。修好之后重新打开应用即可。").
				FontSize(11).TextColor(theme.TextMuted)

			for i, problem := range vm.StartupProblems {
				vm.problemCard(c, i, problem)
			}

			if vm.StartupError != "" {
				components.Text(c, vm.StartupError).FontSize(11).TextColor(theme.Warning)
			}

			ui.Row(c).FillWidth().Gap(theme.Space(1)).AlignItems(ui.Center).Children(func() {
				if vm.canContinue() && components.Button(c, "用默认设置继续", components.ButtonPrimary).Clicked() {
					vm.StartupOpen = false
					vm.OpenDevices()
				}
				if components.Button(c, "退出", components.ButtonOutline).Clicked() && vm.Quit != nil {
					vm.Quit()
				}
			})
		})
	})
}

// problemCard is one problem: what failed, what the failure said, where to
// look, and the one action that fixes it.
func (vm *ViewModel) problemCard(c *ui.Context, index int, problem core.Problem) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(theme.Space(0.75)).
		Padding(theme.Space(1.5)).Radius(theme.Radius).Border(1, theme.Border).
		Children(func() {
			ui.Row(c).FillWidth().Gap(theme.Space(1)).AlignItems(ui.Center).
				Justify(ui.SpaceBetween).Children(func() {
				components.Text(c, problem.Title).FontSize(13).Bold()
				vm.problemAction(c, index, problem)
			})
			if problem.Detail != "" {
				components.Text(c, problem.Detail).FontSize(11).TextColor(theme.TextMuted)
			}
			if problem.Path != "" {
				components.Text(c, problem.Path).FontSize(11).Font("monospace").TextColor(theme.TextMuted)
			}
		})
}

// problemAction is the button that fixes one problem. Problems the app cannot
// do anything about (a missing keychain, say) offer none, rather than a button
// that opens a page where nothing can be set.
func (vm *ViewModel) problemAction(c *ui.Context, index int, problem core.Problem) {
	switch problem.Kind {
	case core.ProblemConfig, core.ProblemHistory, core.ProblemAppMeta:
		if vm.OpenDir == nil || problem.Path == "" {
			return
		}
		if components.Button(c, "打开配置目录", components.ButtonOutline).Clicked() {
			if err := vm.OpenDir(problem.Path); err != nil {
				vm.StartupError = err.Error()
			} else {
				vm.StartupError = ""
			}
		}
	case core.ProblemADB:
		// The tool settings is where an adb path is entered; the page steps
		// aside rather than sitting on top of the fix.
		if components.Button(c, "打开工具设置", components.ButtonOutline).Clicked() {
			vm.StartupOpen = false
			vm.OpenTools()
		}
	}
}

// canContinue reports whether every problem leaves the app usable. A fatal one
// would hide the button: carrying on would only produce a window that cannot
// do its job.
func (vm *ViewModel) canContinue() bool {
	for _, problem := range vm.StartupProblems {
		if !problem.Continue {
			return false
		}
	}
	return true
}
