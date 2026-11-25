package app

import (
	"context"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/oneo-me/onevra/source/shells/sidebar"
)

// ContentView is the routed content area: a device index page, or the page of
// one device reached from the sidebar or a breadcrumb.
func (vm *ViewModel) ContentView(c *ui.Context) {
	for _, toast := range vm.drainToasts() {
		c.AddToast(toast)
	}
	ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), 0, 0, 0).Children(func() {
		if vm.StartupOpen {
			vm.startupPage(c)
			return
		}
		vm.routedPage(c)
	})
	if vm.StartupOpen {
		// The dialogs belong to the pages the startup page replaced.
		return
	}
	vm.EditorView(c)
	vm.TransferView(c)
	vm.FileDetailView(c)
	vm.ReachabilityView(c)
	vm.AttentionDialog(c)
	vm.LogOutputView(c)
	vm.LogClearView(c)
	vm.DerpEditorView(c)
	vm.DerpDeleteView(c)
	vm.CacheConfirmView(c)
	vm.APKView(c)
	vm.InstallView(c)
}

// routedPage builds the page the router shows.
func (vm *ViewModel) routedPage(c *ui.Context) {
	// Nothing is following until a log view says so this frame; that is how a
	// page left behind stops polling.
	vm.clearActiveLog()
	vm.beginLogcatFrame()
	// A stream left behind by a frame that did not draw it must stop: it reads
	// the device channel. The defer runs on every return of this function.
	defer vm.settleLogcat()
	if vm.Router == nil {
		if profile, ok := vm.SelectedProfile(); ok {
			vm.devicePage(c, profile)
		} else {
			vm.deviceIndexPage(c)
		}
		return
	}
	// The tools draft belongs to one visit: leaving the page drops it, so
	// coming back starts from what is saved rather than a forgotten edit.
	if vm.Router.Path() != SettingsRoute {
		vm.SettingsLoaded = false
	}
	// A management page shows no device page, so no sidebar row stays tinted:
	// a highlighted E245 next to 缓存管理 reads as "these are E245's". The
	// cache page keeps the device the user came from, since it opens on the
	// selected device's entries the first time.
	if _, isManage := vm.managePageTrail(vm.Router.Path()); isManage {
		vm.dropSelection(vm.Router.Path())
	}
	vm.Router.View(c, func(r *ui.Route) {
		switch {
		case r.Match(CacheRoute):
			r.Title("缓存管理")
			vm.cachePage(c)
			return
		case r.Match(SettingsRoute):
			r.Title("工具设置")
			vm.settingsPage(c)
			return
		case r.Match(LogsRoute):
			r.Title("日志")
			vm.logsPage(c)
			return
		case r.Match(LogcatRoute):
			r.Title("Logcat")
			vm.logcatPage(c)
			return
		case r.Match(DerpRoute):
			r.Title("DERP 节点")
			vm.derpListPage(c)
			return
		case r.Match(DerpRoute + "/{id}"):
			if trail, isManage := vm.managePageTrail(r.Path()); isManage {
				r.Title(trail[len(trail)-1])
			}
			vm.derpDetailPage(c, r.Param("id"))
			return
		}
		if r.Match("/devices/{id}") {
			profile, ok := vm.profileByID(r.Param("id"))
			if !ok {
				r.Title("设备不存在")
				vm.missingDevicePage(c)
				return
			}
			// The route is the source of truth: coming back to a page
			// selects the device it shows.
			vm.Selection = profile.ID
			r.Title(profile.Name)
			vm.devicePage(c, profile)
			return
		}
		// The index page shows no device, so nothing is selected.
		vm.Selection = uuid.Nil
		r.Title("首页")
		vm.toolbar(c)
		vm.deviceIndexPage(c)
	})
}

// dropSelection clears the sidebar's tint when a page that shows no device is
// opened. The cache page keeps the device it was opened from, because it shows
// that device's entries; every other management page just drops it.
func (vm *ViewModel) dropSelection(path string) {
	if path == CacheRoute && vm.CacheDevice == uuid.Nil {
		vm.CacheDevice = vm.Selection
	}
	vm.Selection = uuid.Nil
}

// deviceIndexPage lists the devices; it is what the first breadcrumb returns to.
func (vm *ViewModel) deviceIndexPage(c *ui.Context) {
	theme, profiles := c.Theme(), vm.Manager.AllProfiles()
	ui.Column(c).Fill().Padding(20).Gap(10).Children(func() {
		if len(profiles) == 0 {
			components.Text(c, "连接你的第一台设备").FontSize(13)
			components.Text(c, "每台设备拥有独立通道，支持同时连接。").FontSize(11).TextColor(theme.TextMuted)
			if components.Button(c, "添加设备", components.ButtonPrimary).Clicked() {
				vm.openEditor(&EditorRequest{})
			}
			return
		}
		for _, profile := range profiles {
			vm.deviceCard(c, profile)
		}
	})
}

// deviceCard is one row of the index page.
func (vm *ViewModel) deviceCard(c *ui.Context, profile core.Profile) {
	theme := c.Theme()
	session := vm.Manager.Session(profile.ID)
	row := ui.ButtonBase(c)
	row.FillWidth().Shrink(0).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).
		Justify(ui.Start).AlignItems(ui.Center).Radius(theme.Radius).Border(1, theme.Border)
	transport := vm.transportLine(profile, session)
	row.Children(func() {
		statusDot(c, session.State)
		ui.Column(c).Gap(1).Grow(1).Children(func() {
			components.Tooltip(c, components.Text(c, profile.Name).Bold().SingleLine(), profile.Name)
			components.Tooltip(c, components.Text(c, transport).FontSize(11).SingleLine(), transport)
		})
		components.Text(c, session.State.Label()).FontSize(11).TextColor(theme.TextMuted)
	})
	if row.Clicked() {
		vm.OpenDevice(profile.ID)
	}
}

// transportLine says how a device is reached, and where.
func (vm *ViewModel) transportLine(profile core.Profile, session core.Session) string {
	switch {
	case profile.DirectADB:
		return "本地 ADB 直连 · " + orPlaceholder(profile.DirectTarget, "未填地址")
	case session.Port != 0:
		return "通过 Tailcat 转发 · 本地端口 " + itoa(session.Port)
	}
	return "通过 Tailcat 转发"
}

// missingDevicePage shows a device that is gone from the configuration.
func (vm *ViewModel) missingDevicePage(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		components.Text(c, "设备不存在").FontSize(18).Bold()
		components.Text(c, "它可能已被删除，或来自上一次运行。").FontSize(11)
		if components.Button(c, "返回我的设备", components.ButtonPrimary).Clicked() {
			vm.OpenDevices()
		}
	})
}

// toolbar holds the window-wide actions.
func (vm *ViewModel) toolbar(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(0, 16, 8, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		// The primary action comes first; tool settings lives in the sidebar
		// footer, where the tools it configures are shown.
		if actionButton(c, lucide.Plus, "添加设备", components.ButtonPrimary).Clicked() {
			vm.openEditor(&EditorRequest{})
		}
		if actionButton(c, lucide.Zap, "临时连接", components.ButtonSecondary).Clicked() {
			vm.openEditor(&EditorRequest{Temporary: true})
		}
		if actionButton(c, lucide.Radar, "测试全部设备", components.ButtonSecondary).Clicked() {
			vm.openReachability()
		}
		if vm.Demo {
			components.Text(c, "界面演示 · 使用模拟设备，不执行真实命令").FontSize(11)
		}
	})
}

func (vm *ViewModel) devicePage(c *ui.Context, profile core.Profile) {
	manager := vm.Manager
	session := manager.Session(profile.ID)
	temporary := isTemporary(manager, profile.ID)

	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				components.Text(c, profile.Name).FontSize(20).Bold()
				subtitle := "通过 Tailcat 转发 · 独立连接会话"
				if profile.DirectADB {
					subtitle = "本地 ADB 直连 · " + orPlaceholder(profile.DirectTarget, "未填地址")
				}
				if temporary {
					if profile.DirectADB {
						subtitle = "本地 ADB 直连 · 临时设备 · 仅本次运行"
					} else {
						subtitle = "通过 Tailcat 转发 · 临时设备 · 仅本次运行"
					}
				}
				components.Text(c, subtitle).FontSize(11)
			})
			if components.Button(c, "编辑", components.ButtonOutline).Clicked() {
				edited := profile
				vm.openEditor(&EditorRequest{Profile: &edited})
			}
			if temporary {
				if components.Button(c, "保存到我的设备", components.ButtonOutline).Clicked() {
					vm.promote(profile)
				}
			}
			connectLabel := "连接"
			if session.Serial != "" {
				connectLabel = "重新连接"
			}
			if components.Button(c, connectLabel, components.ButtonPrimary).Clicked() {
				go func() { vm.Fail(manager.Connect(context.Background(), profile.ID)) }()
			}
			if components.Button(c, "断开", components.ButtonSecondary).Clicked() {
				go manager.Disconnect(context.Background(), profile.ID)
			}
		})
		components.Tabs(c, &vm.DetailTab, components.TabsSegmented, "概览", "资源", "终端", "文件", "日志")
		switch vm.DetailTab {
		case detailResources:
			vm.resourcePage(c, profile, session)
		case detailTerminal:
			// Leaving the resource tab ends its polling.
			vm.Manager.StopResourceSampling(profile.ID)
			components.Tabs(c, &vm.ConsoleTab, components.TabsSegmented, "设备 Shell", "ADB 命令")
			vm.consoleView(c, profile, session)
		case detailFiles:
			vm.Manager.StopResourceSampling(profile.ID)
			vm.filesPage(c, profile, session)
		case detailLog:
			vm.Manager.StopResourceSampling(profile.ID)
			vm.logPage(c, profile)
		default:
			vm.Manager.StopResourceSampling(profile.ID)
			vm.overview(c, profile, session)
		}
	})
}

func (vm *ViewModel) overview(c *ui.Context, profile core.Profile, session core.Session) {
	manager := vm.Manager
	ui.Column(c).Fill().Gap(6).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			statusDot(c, session.State)
			components.Text(c, session.State.Label()).Bold()
		})
		if session.Err != "" {
			components.Text(c, session.Err).FontSize(11)
		}
		if profile.DirectADB {
			// No Tailcat channel, so no remote port and no relay: showing
			// them is what made a direct device look misconfigured.
			detailRow(c, "连接方式", "本地 ADB 直连")
			detailRow(c, "ADB 地址", orPlaceholder(profile.DirectTarget, "未填地址"))
			detailRow(c, "本地入口", orPlaceholder(session.Serial, "未分配"))
		} else {
			detailRow(c, "连接方式", "通过 Tailcat 转发")
			detailRow(c, "本地入口", orPlaceholder(session.Serial, "未分配"))
			detailRow(c, "远端端口", itoa(profile.RemotePort))
			detailRow(c, "DERP 中继", relayLabel(vm, profile))
		}
		detailRow(c, "链路", session.Route)
		detailRow(c, "设备型号", session.Model)
		detailRow(c, "设备代号", session.Product)
		detailRow(c, "Android", session.Android)
		detailRow(c, "设备序列号", session.DeviceSerial)
		detailBoxRow(c, "Shell 权限", session.ShellID)
		vm.deviceUsersSection(c, session.Users)

		ui.Row(c).Gap(6).Children(func() {
			// Diagnosing measures the Tailcat channel, which a direct
			// device does not have.
			if !profile.DirectADB {
				if components.Button(c, "探测链路", components.ButtonSecondary).Clicked() {
					go func() { vm.Fail(manager.Diagnose(context.Background(), profile.ID)) }()
				}
			}
			if components.Button(c, "刷新信息", components.ButtonSecondary).Clicked() {
				go func() { vm.Fail(manager.RefreshDeviceInformation(context.Background(), profile.ID)) }()
			}
			if profile.DirectADB {
				if components.Button(c, "复制 ADB 地址", components.ButtonOutline).Clicked() && profile.DirectTarget != "" {
					vm.copyText(profile.DirectTarget)
				}
			} else if components.Button(c, "复制 ADB 入口", components.ButtonOutline).Clicked() && session.Serial != "" {
				vm.copyText(session.Serial)
			}
		})

		components.Text(c, "设备操作").FontSize(13).Bold()
		ui.Row(c).Gap(6).Children(func() {
			if components.Button(c, "安装 APK", components.ButtonSecondary).Clicked() {
				vm.chooseAPK(profile)
			}
			if components.Button(c, "上传", components.ButtonSecondary).Clicked() {
				vm.chooseUpload(profile)
			}
			if components.Button(c, "下载", components.ButtonSecondary).Clicked() {
				vm.startDownload(profile)
			}
			if components.Button(c, "屏幕控制", components.ButtonSecondary).Clicked() {
				go func() { _, err := manager.BeginScreen(context.Background(), profile.ID); vm.Fail(err) }()
			}
		})
	})
}

func detailRow(c *ui.Context, label, value string) {
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		ui.Column(c).Width(90).Shrink(0).Children(func() { components.Text(c, label).FontSize(11) })
		// Grow + MinWidth(0) is what lets a long value wrap inside the page:
		// without it the text keeps its intrinsic width and runs to the window
		// edge, where it is clipped with nothing to scroll.
		components.Text(c, value).FontSize(11).Grow(1).MinWidth(0)
	})
}

// detailBoxRow shows a value that is long enough to wrap: `id` on a device is
// one long line of groups. The framed box ends the lines inside the page and
// gives the text room on both sides, which a bare row cannot: the label column
// is fixed, so the value had no right margin at all.
func detailBoxRow(c *ui.Context, label, value string) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(3).Children(func() {
		components.Text(c, label).FontSize(11)
		ui.Box(c).FillWidth().Padding(theme.Space(1), theme.Space(1.5)).
			Radius(theme.Radius).Border(1, theme.Border).Children(func() {
			// An empty box reads as a rendering bug; the placeholder says the
			// value was not read yet.
			components.Text(c, orPlaceholder(value, core.PlaceholderUnknown)).FontSize(11).Font("monospace")
		})
	})
}

// deviceUsersSection lists the device's Android profiles. It is a table rather
// than one line of text because each profile carries three things the user
// compares across users — the number adb takes, its name, and which one is
// running now — and a "·"-joined sentence made that unreadable past two users.
func (vm *ViewModel) deviceUsersSection(c *ui.Context, users []core.DeviceUser) {
	theme := c.Theme()
	components.Text(c, "设备用户").FontSize(13).Bold()
	if len(users) == 0 {
		components.Text(c, core.PlaceholderUnknown).FontSize(11).TextColor(theme.TextMuted)
		return
	}
	tableCard(c, &vm.UserTable, userColumns(), len(users), tableOptions{
		Label: "设备用户表", Rows: len(users),
	}, func(row, col int) {
		user := users[row]
		switch col {
		case 0:
			components.Text(c, itoa(user.ID)).FontSize(11)
		case 1:
			components.Text(c, user.Name).FontSize(11).SingleLine()
		default:
			if user.Current {
				cellBadge(c, deviceUserStatus(user), components.BadgeSuccess)
				return
			}
			components.Text(c, deviceUserStatus(user)).FontSize(11).TextColor(theme.TextMuted)
		}
	})
}

// userColumns are the profile table's columns: what adb takes, what the user
// calls it, and whether it is running.
func userColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "用户", ID: "user", Width: 70},
		{Title: "名称", ID: "name", MinWidth: 140},
		{Title: "状态", ID: "state", Width: 90},
	}
}

// deviceUserStatus says which of the three things is true of a profile. A
// device reports both a running flag and the current user; only one profile can
// be current, so 当前用户 wins.
func deviceUserStatus(user core.DeviceUser) string {
	switch {
	case user.Current:
		return "当前用户"
	case user.Running:
		return "运行中"
	}
	return "未启动"
}

func orPlaceholder(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func relayLabel(vm *ViewModel, profile core.Profile) string {
	if profile.RelaySource == "" {
		return "默认节点"
	}
	for _, preset := range vm.Manager.RelayPresets() {
		if preset.Source == profile.RelaySource {
			return preset.Name
		}
	}
	return "自定义 JSON 链接或内容"
}
