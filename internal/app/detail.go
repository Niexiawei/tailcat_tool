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
		vm.routedPage(c)
	})
	vm.EditorView(c)
	vm.TransferView(c)
	vm.FileDetailView(c)
	vm.SettingsView(c)
	vm.ReachabilityView(c)
	vm.AttentionDialog(c)
}

// routedPage builds the page the router shows.
func (vm *ViewModel) routedPage(c *ui.Context) {
	if vm.Router == nil {
		if profile, ok := vm.SelectedProfile(); ok {
			vm.devicePage(c, profile)
		} else {
			vm.deviceIndexPage(c)
		}
		return
	}
	vm.Router.View(c, func(r *ui.Route) {
		if name, isManage := managePageTitle(r.Path()); isManage {
			r.Title(name)
			vm.logsPage(c)
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
		r.Title("我的设备")
		vm.toolbar(c)
		vm.deviceIndexPage(c)
	})
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
			vm.logPage(c, profile, session)
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
		detailRow(c, "Shell 权限", session.ShellID)

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
		components.Text(c, value).FontSize(11)
	})
}

func orPlaceholder(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func relayLabel(vm *ViewModel, profile core.Profile) string {
	if profile.RelaySource == "" {
		return "官方默认节点"
	}
	for _, preset := range vm.Manager.RelayPresets() {
		if preset.Source == profile.RelaySource {
			return preset.Name
		}
	}
	return "自定义 JSON 链接或内容"
}
