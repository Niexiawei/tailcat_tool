package app

import (
	"context"
	"log"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/google/uuid"
)

// TrayIcons are the two tray images. Light is drawn on a light bar, so its
// ink is dark; Dark is drawn on a dark bar, so its ink is light. A single
// image would vanish on one of them.
type TrayIcons struct {
	Light []byte
	Dark  []byte
}

// forBar picks the image for the bar: dark reports whether the bar is dark.
func (i TrayIcons) forBar(dark bool) []byte {
	if dark {
		return i.Dark
	}
	return i.Light
}

// applyTrayIcon sets the image that matches the bar the icon shows on.
func applyTrayIcon(tray *mygo.Tray, icons TrayIcons) {
	if err := tray.SetIcon(icons.forBar(tray.Dark()), false); err != nil {
		log.Printf("tray icon: %v", err)
	}
}

// TrayMenu builds the tray menu from the current manager state. It is rebuilt
// on every change so the labels always describe the live channels.
func (vm *ViewModel) TrayMenu(show func(), quit func()) *mygo.Menu {
	items := []*mygo.MenuItem{
		{Label: "打开管理窗口", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
		mygo.Separator(),
	}
	for _, profile := range vm.Manager.AllProfiles() {
		profile := profile
		session := vm.Manager.Session(profile.ID)
		label := profile.Name + " · "
		if isTemporary(vm.Manager, profile.ID) {
			label += "临时 · "
		}
		label += session.State.Label()
		var submenu []*mygo.MenuItem
		connectLabel := "连接"
		if session.Serial != "" {
			connectLabel = "重新连接"
		}
		submenu = append(submenu, &mygo.MenuItem{
			Label:    connectLabel,
			Disabled: session.State.Busy(),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() { _ = vm.Manager.Connect(context.Background(), profile.ID) }()
			},
		})
		submenu = append(submenu, &mygo.MenuItem{
			Label:    "断开",
			Disabled: session.Serial == "",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go vm.Manager.Disconnect(context.Background(), profile.ID)
			},
		})
		if isTemporary(vm.Manager, profile.ID) {
			submenu = append(submenu, &mygo.MenuItem{
				Label: "关闭临时设备",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					go func() { _ = vm.Manager.RemoveDevice(context.Background(), profile.ID) }()
				},
			})
		}
		items = append(items, &mygo.MenuItem{Label: label, Submenu: submenu})
	}
	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{
			Label:    "断开应用管理的全部连接",
			Disabled: vm.Manager.OwnedActiveCount() == 0,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go vm.Manager.DisconnectAll(context.Background())
			},
		},
		&mygo.MenuItem{Label: "退出 " + AppName, Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) { quit() }},
	)
	return mygo.NewMenu(items)
}

func isTemporary(m *core.Manager, id uuid.UUID) bool {
	for _, p := range m.TemporaryProfiles() {
		if p.ID == id {
			return true
		}
	}
	return false
}
