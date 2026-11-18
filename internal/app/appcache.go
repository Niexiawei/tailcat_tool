package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// cachePage lists the application names and icons cached per device. Task 3
// fills in the picker and the table; the route and the breadcrumb land here so
// the plan's three management pages share one skeleton.
func (vm *ViewModel) cachePage(c *ui.Context) {
	ui.Column(c).Fill().Padding(20).Gap(10).Children(func() {
		components.Text(c, "应用信息缓存").FontSize(18).Bold()
	})
}
