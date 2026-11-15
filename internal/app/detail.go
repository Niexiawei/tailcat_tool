package app

import (
	"context"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/shells/sidebar"
)

// ContentView is the detail pane for the selected device.
func (vm *ViewModel) ContentView(c *ui.Context) {
	vm.toolbar(c)
	profile, ok := vm.SelectedProfile()
	if ok {
		vm.detail(c, profile)
	} else {
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			components.Text(c, "连接你的第一台设备").FontSize(20).Bold()
			components.Text(c, "每台设备拥有独立通道，支持同时连接。").FontSize(12)
			if components.Button(c, "添加设备", components.ButtonPrimary).Clicked() {
				vm.openEditor(&EditorRequest{})
			}
		})
	}
	vm.TaskPanel(c)
	vm.EditorView(c)
	vm.TransferView(c)
	vm.SettingsView(c)
	vm.ReachabilityView(c)
	vm.AttentionDialog(c)
	if note := vm.Notice; note != "" {
		ui.Row(c).FillWidth().Padding(12, 16).Gap(8).Children(func() {
			components.Text(c, note).FontSize(11)
			if components.Button(c, "关闭", components.ButtonGhost).Clicked() {
				vm.Notice = ""
			}
		})
	}
}

// toolbar holds the window-wide actions.
func (vm *ViewModel) toolbar(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(sidebar.HeaderHeight(c), 16, 8, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		if components.Button(c, "测试全部设备", components.ButtonGhost).Clicked() {
			vm.openReachability()
		}
		if components.Button(c, "临时连接", components.ButtonGhost).Clicked() {
			vm.openEditor(&EditorRequest{Temporary: true})
		}
		if components.Button(c, "添加设备", components.ButtonGhost).Clicked() {
			vm.openEditor(&EditorRequest{})
		}
		if components.Button(c, "工具设置", components.ButtonGhost).Clicked() {
			vm.openSettings()
		}
		if vm.Demo {
			components.Text(c, "界面演示 · 使用模拟设备，不执行真实命令").FontSize(11)
		}
	})
}

func (vm *ViewModel) detail(c *ui.Context, profile core.Profile) {
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
			if temporary {
				if components.Button(c, "保存到我的设备", components.ButtonGhost).Clicked() {
					vm.promote(profile)
				}
			}
			connectLabel := "连接"
			if session.Serial != "" {
				connectLabel = "重新连接"
			}
			if components.Button(c, connectLabel, components.ButtonPrimary).Clicked() {
				go func() { _ = manager.Connect(context.Background(), profile.ID) }()
			}
			if components.Button(c, "断开", components.ButtonSecondary).Clicked() {
				go manager.Disconnect(context.Background(), profile.ID)
			}
		})
		components.Tabs(c, &vm.DetailTab, components.TabsSegmented, "概览", "日志")
		if vm.DetailTab == 0 {
			vm.overview(c, profile, session)
		} else {
			vm.logs(c, session)
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
			if components.Button(c, "探测链路", components.ButtonSecondary).Clicked() {
				go func() { _ = manager.Diagnose(context.Background(), profile.ID) }()
			}
			if components.Button(c, "刷新信息", components.ButtonSecondary).Clicked() {
				go func() { _ = manager.RefreshDeviceInformation(context.Background(), profile.ID) }()
			}
			if profile.DirectADB {
				if components.Button(c, "复制 ADB 地址", components.ButtonGhost).Clicked() && profile.DirectTarget != "" {
					vm.copyText(profile.DirectTarget)
				}
			} else if components.Button(c, "复制 ADB 入口", components.ButtonGhost).Clicked() && session.Serial != "" {
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
				go func() { _, _ = manager.BeginScreen(context.Background(), profile.ID) }()
			}
			if components.Button(c, "终端", components.ButtonSecondary).Clicked() {
				vm.openTerminal(profile)
			}
		})

		ui.Row(c).FillWidth().Gap(6).Children(func() {
			box := components.TextBox(c, &vm.ShellCommand, components.TextBoxOptions{})
			box.Label("执行一条 ADB Shell 命令")
			box.Placeholder("执行一条 ADB Shell 命令")
			if components.Button(c, "执行", components.ButtonPrimary).Clicked() && vm.ShellCommand != "" {
				command := vm.ShellCommand
				vm.ShellCommand = ""
				go vm.runShell(profile, command)
			}
		})
		components.Text(c, "任务始终绑定启动时的设备和 ADB 入口。").FontSize(11)
	})
}

func (vm *ViewModel) logs(c *ui.Context, session core.Session) {
	ui.Column(c).Fill().Gap(3).Children(func() {
		if len(session.Logs) == 0 {
			components.Text(c, "暂无通道日志").FontSize(11)
			return
		}
		for _, entry := range session.Logs {
			ui.Row(c).Gap(8).Children(func() {
				components.Text(c, entry.At.Format("15:04:05")).FontSize(11)
				components.Text(c, entry.Text).FontSize(11)
			})
		}
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
