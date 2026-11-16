package app

import (
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// openReachability shows the table. Probing starts only when the user asks for
// it: opening the dialog on a machine with many devices must not spend the
// first frames queueing dozens of rows nobody requested.
func (vm *ViewModel) openReachability() {
	vm.ReachabilityOpen = true
}

// closeReachability closes the dialog and stops a batch it started, since
// results the user walked away from are not worth the probes.
func (vm *ViewModel) closeReachability() {
	vm.ReachabilityOpen = false
	if checker := vm.Manager.Reachability(); checker.Running() {
		checker.Cancel()
	}
}

// ReachabilityView is the batch reachability table.
func (vm *ViewModel) ReachabilityView(c *ui.Context) {
	theme := c.Theme()
	open := vm.ReachabilityOpen
	components.Dialog(c, &open, components.DialogOptions{Label: "全部设备连通测试"}, func(panel ui.Element) {
		panel.Padding(18).Gap(10).Width(760)
		panel.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				components.Text(c, "全部设备连通测试").FontSize(18).Bold()
				if components.IconButton(c, lucide.X(), "关闭", components.IconButtonPlain).Clicked() {
					vm.closeReachability()
					open = false
				}
			})
			components.Text(c, "测试每台设备的 Tailcat ping，每次探测最长 8 秒，同时测试 3 台；不会建立或断开 ADB 连接。关闭对话框会中止正在进行的测试。").
				FontSize(11).TextColor(theme.TextMuted)
			checker := vm.Manager.Reachability()
			results := checker.SortedResults()
			running := checker.Running()
			if len(results) == 0 {
				components.Text(c, "还没有测试过。点「开始测试」开始，结果会保留到下一次测试。").
					FontSize(11).TextColor(theme.TextMuted)
			} else {
				reachabilityTable(c, results)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				label := "开始测试"
				if running {
					label = "测试中…"
				}
				if actionButton(c, lucide.Play, label, components.ButtonPrimary).Disabled(running).Clicked() {
					vm.Manager.TestAllDevices()
				}
				if running {
					components.Text(c, reachProgress(results)).FontSize(11).TextColor(theme.TextMuted)
				}
			})
		})
	})
	if !open {
		vm.closeReachability()
	}
}

// reachabilityTable renders one row per device: the state cell carries the icon
// that says pass (green check) or fail (red cross) without reading the words.
func reachabilityTable(c *ui.Context, results []core.ReachabilityResult) {
	theme := c.Theme()
	columns := []ui.TableColumn{
		{Title: "设备", ID: "device", MinWidth: 160},
		{Title: "Tailcat ping", ID: "state", Width: 150},
		{Title: "延迟", ID: "latency", Width: 80, Align: ui.End},
		{Title: "路径 / 详情", ID: "detail", MinWidth: 220},
	}
	components.Table(c, nil, columns, func() {
		for _, result := range results {
			result := result
			components.TableRow(c, result.ID, result.Name+" "+result.State.Label(), false, func(col int) {
				switch col {
				case 0:
					components.Text(c, result.Name).SingleLine()
				case 1:
					icon, color := reachStatus(theme, result.State)
					ui.Row(c).AlignItems(ui.Center).Gap(theme.Space(1)).Children(func() {
						components.Icon(c, icon).Size(theme.Rem(.875), theme.Rem(.875)).TextColor(color)
						components.Text(c, result.State.Label()).FontSize(11).TextColor(color).SingleLine()
					})
				case 2:
					components.Text(c, result.Latency).SingleLine()
				case 3:
					components.Text(c, reachDetail(result)).SingleLine()
				}
			})
		}
	})
}

// reachStatus is the icon and colour the table draws for one probe state:
// green check for reachable, red cross for anything that failed.
func reachStatus(theme *ui.Theme, state core.ReachabilityState) (func() *ui.SVG, ui.Color) {
	switch state {
	case core.ReachReachable:
		return lucide.CircleCheck, theme.Success
	case core.ReachUnreachable, core.ReachFailed:
		return lucide.CircleX, theme.Danger
	case core.ReachTesting:
		return lucide.Loader, theme.Accent
	case core.ReachCancelled:
		return lucide.CircleSlash, theme.TextMuted
	default:
		return lucide.Circle, theme.TextMuted
	}
}

// reachDetail is the last column: the route when the probe found one, else the
// error the probe reported.
func reachDetail(result core.ReachabilityResult) string {
	if result.Route == "" || result.Route == "—" {
		return result.Detail
	}
	return result.Route
}

// reachProgress counts finished rows for the running indicator.
func reachProgress(results []core.ReachabilityResult) string {
	done := 0
	for _, result := range results {
		if result.State != core.ReachQueued && result.State != core.ReachTesting {
			done++
		}
	}
	return "已测试 " + itoa(done) + "/" + itoa(len(results))
}
