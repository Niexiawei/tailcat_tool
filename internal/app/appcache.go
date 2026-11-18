package app

import (
	"strconv"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// cachePage lists the application names and icons cached per device. It only
// ever reads and deletes: a name appears here because the resource page asked
// for it, not because this page fetches.
func (vm *ViewModel) cachePage(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		components.Text(c, "应用信息缓存").FontSize(18).Bold()
		if !vm.Manager.AppMetaCacheAvailable() {
			components.Text(c, "应用信息缓存不可用，资源页只显示包名").FontSize(11).TextColor(theme.Warning)
			return
		}
		profiles := vm.Manager.AllProfiles()
		if len(profiles) == 0 {
			components.Text(c, "还没有设备，缓存会在资源页读取应用信息时产生").FontSize(11).TextColor(theme.TextMuted)
			return
		}
		vm.ensureCacheDevice()
		vm.cacheDevicePicker(c, profiles)

		entries := vm.Manager.AppMetaCacheEntries(vm.CacheDevice)
		if len(entries) == 0 {
			components.Text(c, "这台设备还没有缓存任何应用信息").FontSize(11).TextColor(theme.TextMuted)
			return
		}
		var total int64
		for _, entry := range entries {
			total += entry.Size
		}
		components.Text(c, itoa(len(entries))+" 条 · 约 "+formatBytes(total)).FontSize(11).TextColor(theme.TextMuted)
		components.Table(c, &vm.CacheTable, cacheColumns(), func() {
			for _, entry := range entries {
				entry := entry
				components.TableRow(c, entry.Package, entryLabel(entry), false, func(col int) {
					vm.cacheCell(c, col, entry)
				})
			}
		})
	})
}

// ensureCacheDevice keeps the picker on a device that still exists: a removed
// device must not leave the page pointing at nothing.
func (vm *ViewModel) ensureCacheDevice() {
	for _, profile := range vm.Manager.AllProfiles() {
		if profile.ID == vm.CacheDevice {
			return
		}
	}
	if vm.Selection != uuid.Nil {
		vm.CacheDevice = vm.Selection
	}
	if vm.CacheDevice == uuid.Nil {
		if profiles := vm.Manager.AllProfiles(); len(profiles) > 0 {
			vm.CacheDevice = profiles[0].ID
		}
	}
}

// cacheDevicePicker chooses the device; temporary devices are listed too, so an
// entry never disappears from the menu because its cache was cleared.
func (vm *ViewModel) cacheDevicePicker(c *ui.Context, profiles []core.Profile) {
	sel := components.Select(c, &vm.CacheDevice)
	sel.Trigger.Width(280)
	sel.Trigger.Children(func() {
		components.Text(c, vm.cacheDeviceLabel(vm.CacheDevice)).SingleLine()
	})
	sel.Popup(func(ui.Element) {
		for _, profile := range profiles {
			item := sel.Item(profile.ID)
			item.Children(func() { components.Text(c, vm.cacheDeviceLabel(profile.ID)).SingleLine() })
		}
	})
}

func (vm *ViewModel) cacheDeviceLabel(id uuid.UUID) string {
	for _, profile := range vm.Manager.AllProfiles() {
		if profile.ID == id {
			return profile.Name + " · " + vm.Manager.Session(id).State.Label()
		}
	}
	return "选择设备"
}

func cacheColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "应用名", ID: "label", MinWidth: 160},
		{Title: "包名", ID: "package", MinWidth: 220},
		{Title: "大小", ID: "size", Width: 90, Align: ui.End},
		{Title: "读取时间", ID: "fetched", Width: 140},
		{Title: "状态", ID: "state", Width: 90},
		{Title: "", ID: "actions", Width: 52},
	}
}

// cacheCell draws one cell of one cached row.
func (vm *ViewModel) cacheCell(c *ui.Context, col int, entry core.AppMetaEntry) {
	theme := c.Theme()
	switch col {
	case 0:
		label := components.Text(c, entryLabel(entry)).FontSize(11).SingleLine()
		components.Tooltip(c, label, entryLabel(entry))
	case 1:
		components.Tooltip(c, components.Text(c, entry.Package).FontSize(11).SingleLine(), entry.Package)
	case 2:
		components.Text(c, formatBytes(entry.Size)).FontSize(11)
	case 3:
		stamp := "—"
		if !entry.FetchedAt.IsZero() {
			stamp = entry.FetchedAt.Format("2006-01-02 15:04")
		}
		components.Text(c, stamp).FontSize(11)
	case 4:
		if entry.Stale {
			components.Badge(c, "已过期", components.BadgeOptions{Tone: components.BadgeWarning})
			return
		}
		components.Text(c, "有效").FontSize(11).TextColor(theme.TextMuted)
	default:
		components.IconButton(c, lucide.Trash(), "删除", components.IconButtonPlain)
	}
}

// entryLabel is what the user calls the row: the name the APK carries, or the
// package while it has not been read yet.
func entryLabel(entry core.AppMetaEntry) string {
	if entry.Label != "" {
		return entry.Label
	}
	return entry.Package
}

// formatBytes renders a size with one decimal in 1024-based units. It is local
// on purpose: pulling in a humanize dependency for one line is not worth it.
func formatBytes(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}
	value := float64(size)
	for _, unit := range []string{"KB", "MB", "GB"} {
		value /= 1024
		if value < 1024 || unit == "GB" {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + unit
		}
	}
	return strconv.FormatInt(size, 10) + " B"
}
