package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// manageEntries is the browsing group under the device list: pages, not window
// actions, so they live inside the scrolling sidebar and scroll with it.
//
// The management pages add their rows here; the device groups come first
// because the user asked for these below them.
func (vm *ViewModel) manageEntries(c *ui.Context) {
	vm.manageEntry(c, lucide.Database, "缓存管理", CacheRoute)
	vm.manageEntry(c, lucide.Server, "DERP 节点", DerpRoute)
	vm.manageEntry(c, lucide.ScrollText, "日志", LogsRoute)
	// The last row is the one thing that configures the app itself rather than
	// showing its data; it sits at the end so the data pages stay first.
	vm.manageEntry(c, lucide.Settings, "工具设置", SettingsRoute)
}

// manageEntry is one row of that group. It is built like a device row rather
// than like a button: same padding, same left edge, same selected tint, so the
// two groups read as one list of places to go.
func (vm *ViewModel) manageEntry(c *ui.Context, icon func() *ui.SVG, label, route string) {
	theme := c.Theme()
	selected := vm.Router != nil && vm.Router.Path() == route
	row := ui.ButtonBase(c)
	row.FillWidth().Shrink(0).Padding(theme.Space(1), theme.Space(1.5)).Gap(theme.Space(1.5)).
		Justify(ui.Start).AlignItems(ui.Center).Radius(theme.Radius)
	if selected {
		row.Background(selectedRowTint(theme))
	}
	row.Children(func() {
		components.Icon(c, icon).Width(14).Height(14)
		components.Text(c, label).SingleLine()
	})
	if row.Clicked() && vm.Router != nil {
		vm.Router.Push(route)
	}
}

// managePageTrail returns the breadcrumb names below 首页 for a management page,
// and false for every other route. A detail page keeps its list in the trail, so
// the title bar always offers the way back to it. The preset's name is read
// every frame, so a rename shows up without the route changing.
func (vm *ViewModel) managePageTrail(path string) ([]string, bool) {
	switch path {
	case CacheRoute:
		return []string{"缓存管理"}, true
	case LogsRoute:
		return []string{"日志"}, true
	case SettingsRoute:
		return []string{"工具设置"}, true
	case DerpRoute:
		return []string{"DERP 节点"}, true
	}
	if rest, ok := strings.CutPrefix(path, DerpRoute+"/"); ok {
		if id, err := uuid.Parse(rest); err == nil && vm.Manager != nil {
			for _, preset := range vm.Manager.RelayPresets() {
				if preset.ID == id {
					return []string{"DERP 节点", preset.Name}, true
				}
			}
		}
		return []string{"DERP 节点"}, true
	}
	return nil, false
}
