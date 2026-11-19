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
		// The table takes the room left and scrolls its own rows: 40 entries on
		// a small window would otherwise push the clear action and the last rows
		// past the bottom edge, where neither the wheel nor the bar can reach
		// them.
		tableCard(c, &vm.CacheTable, cacheColumns(), len(entries), tableOptions{
			Label: "应用信息缓存列表", Grow: true,
		}, func(row, col int) {
			vm.cacheCell(c, col, entries[row])
		})
		if components.Button(c, "清空这台设备的缓存", components.ButtonSecondary).AlignSelf(ui.Start).Shrink(0).Clicked() {
			vm.CacheConfirmOpen = true
			vm.CacheConfirmPkg = ""
			vm.CacheConfirmError = ""
		}
	})
}

// ensureCacheDevice keeps the picker on a device that still exists: a removed
// device must not leave the page pointing at nothing. The first device is a
// bare fallback; the device the user was looking at comes in through
// dropSelection, which runs before this page builds.
func (vm *ViewModel) ensureCacheDevice() {
	profiles := vm.Manager.AllProfiles()
	for _, profile := range profiles {
		if profile.ID == vm.CacheDevice {
			return
		}
	}
	vm.CacheDevice = uuid.Nil
	if len(profiles) > 0 {
		vm.CacheDevice = profiles[0].ID
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

// cacheDeviceName is the bare name, for a sentence that already says 设备.
func (vm *ViewModel) cacheDeviceName(id uuid.UUID) string {
	for _, profile := range vm.Manager.AllProfiles() {
		if profile.ID == id {
			return profile.Name
		}
	}
	return "这台设备"
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
			cellBadge(c, "已过期", components.BadgeWarning)
			return
		}
		components.Text(c, "有效").FontSize(11).TextColor(theme.TextMuted)
	default:
		if components.IconButton(c, lucide.Trash(), "删除", components.IconButtonPlain).Clicked() {
			vm.CacheConfirmOpen = true
			vm.CacheConfirmPkg = entry.Package
			vm.CacheConfirmError = ""
		}
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

// CacheConfirmView asks before one row or a whole device leaves the cache.
// The confirmation states what disappears and that the resource page will read
// it again, because "cleared" is not the same as "gone from the device".
func (vm *ViewModel) CacheConfirmView(c *ui.Context) {
	if !vm.CacheConfirmOpen {
		return
	}
	theme := c.Theme()
	open := true
	components.Dialog(c, &open, components.DialogOptions{Label: "清除应用信息缓存"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(440)
		panel.Children(func() {
			entries := vm.Manager.AppMetaCacheEntries(vm.CacheDevice)
			if vm.CacheConfirmPkg == "" {
				var total int64
				for _, entry := range entries {
					total += entry.Size
				}
				components.Text(c, "清空「"+vm.cacheDeviceName(vm.CacheDevice)+"」的应用信息缓存？").FontSize(18).Bold()
				components.Text(c, "这台设备、"+itoa(len(entries))+" 条、约 "+formatBytes(total)+"。资源页下次打开时会重新读取。").FontSize(11).TextColor(theme.TextMuted)
			} else {
				components.Text(c, "删除「"+vm.cachePackageLabel(entries)+"」的缓存信息？").FontSize(18).Bold()
				components.Text(c, "删除后下一次读取会重新向设备请求。").FontSize(11).TextColor(theme.TextMuted)
			}
			if vm.CacheConfirmError != "" {
				components.Text(c, vm.CacheConfirmError).FontSize(11).TextColor(theme.Warning)
			}
			ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.closeCacheConfirm()
					open = false
				}
				if vm.CacheConfirmPkg == "" {
					if components.Button(c, "确认清空", components.ButtonSecondary).Clicked() {
						if err := vm.purgeAppMetaCache(vm.CacheDevice); err != nil {
							vm.CacheConfirmError = err.Error()
							return
						}
						vm.closeCacheConfirm()
						open = false
					}
				} else {
					if components.Button(c, "确认删除", components.ButtonSecondary).Clicked() {
						if err := vm.Manager.InvalidateAppMetaCache(vm.CacheDevice, vm.CacheConfirmPkg); err != nil {
							vm.CacheConfirmError = err.Error()
							return
						}
						vm.Notify("已删除该应用信息的缓存")
						vm.closeCacheConfirm()
						open = false
					}
				}
			})
		})
	})
	if !open {
		vm.closeCacheConfirm()
	}
}

// cachePackageLabel names the row the confirmation is about.
func (vm *ViewModel) cachePackageLabel(entries []core.AppMetaEntry) string {
	for _, entry := range entries {
		if entry.Package == vm.CacheConfirmPkg {
			return entryLabel(entry)
		}
	}
	return vm.CacheConfirmPkg
}

// purgeAppMetaCache clears one device and the icons this process decoded from
// its old entries.
func (vm *ViewModel) purgeAppMetaCache(device uuid.UUID) error {
	if err := vm.Manager.PurgeAppMetaCache(device); err != nil {
		return err
	}
	vm.forgetAppIcons(device)
	vm.Notify("已清空该设备的应用信息缓存")
	return nil
}

func (vm *ViewModel) closeCacheConfirm() {
	vm.CacheConfirmOpen = false
	vm.CacheConfirmPkg = ""
	vm.CacheConfirmError = ""
}
