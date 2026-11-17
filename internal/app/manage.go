package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// manageEntries is the browsing group under the device list: pages, not window
// actions, so they live inside the scrolling sidebar and scroll with it.
//
// The cache and DERP pages add their own rows here once they exist; today the
// group holds the log page only.
func (vm *ViewModel) manageEntries(c *ui.Context) {
	vm.manageEntry(c, lucide.ScrollText, "日志", LogsRoute)
}

// manageEntry is one row of that group. It is a ghost button so the label
// lines up with the device rows above and the whole row is the target.
func (vm *ViewModel) manageEntry(c *ui.Context, icon func() *ui.SVG, label, route string) {
	theme := c.Theme()
	row := components.Button(c, "", components.ButtonGhost).Label(label)
	row.FillWidth().Shrink(0).Gap(theme.Space(1.5)).Justify(ui.Start)
	if vm.Router != nil && vm.Router.Path() == route {
		row.Background(theme.Selection)
	}
	row.Children(func() {
		components.Icon(c, icon).Width(14).Height(14)
		components.Text(c, label).SingleLine()
	})
	if row.Clicked() && vm.Router != nil {
		vm.Router.Push(route)
	}
}

// managePageTitle returns the breadcrumb name of a management page, and false
// for every other route. ContentTitle falls back to 我的设备 when it is false.
func managePageTitle(path string) (string, bool) {
	switch path {
	case LogsRoute:
		return "日志", true
	}
	return "", false
}
