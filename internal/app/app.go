package app

import (
	"context"
	"log"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/oneo-me/onevra/source/shells/sidebar"
)

// Environment is everything main() has to provide; the window owns the rest.
type Environment struct {
	Manager    *core.Manager
	Locate     core.LocateTool
	Runner     core.Runner
	Install    *core.Installer
	ConfigDir  string
	Notice     string
	Demo       bool
	OnShutdown func()
}

// App is the running window: a view model, its shell and the tray.
type App struct {
	env        Environment
	vm         *ViewModel
	shell      *sidebar.Shell
	tray       *mygo.Tray
	installLog string
}

// New builds the app from the environment.
func New(env Environment) *App {
	vm := NewViewModel(env.Manager, env.Locate)
	vm.Notice = env.Notice
	vm.Demo = env.Demo
	return &App{env: env, vm: vm}
}

// Start opens the window, the tray and the redraw loop.
func (a *App) Start(trayIcon []byte) {
	a.shell = a.vm.OpenMainWindow()
	window := a.shell.Window()
	watchRedraws(a.env.Manager, window)

	if tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "tailcat-manager",
		Icon:           trayIcon,
		IconIsTemplate: true,
		ToolTip:        AppName,
		Menu:           a.vm.TrayMenu(a.Show, a.Quit),
	}); err == nil {
		a.tray = tray
		go a.keepTrayFresh()
	} else {
		log.Printf("tray icon unavailable: %v", err)
	}
	a.vm.Install = a.installComponent
	a.vm.Activate = func(id string) { go a.connectByID(id) }
}

// Show brings the window to the front.
func (a *App) Show() {
	mygo.RunOnMain(func() {
		a.shell.Window().Show()
		a.shell.Window().Focus()
	})
}

// Quit stops the channels and exits.
func (a *App) Quit() {
	a.vm.CloseConsoles()
	if a.env.OnShutdown != nil {
		a.env.OnShutdown()
	}
	mygo.App.Quit()
}

func (a *App) keepTrayFresh() {
	for range a.env.Manager.Changed() {
		menu := a.vm.TrayMenu(a.Show, a.Quit)
		mygo.RunOnMain(func() {
			if a.tray != nil {
				a.tray.SetMenu(menu)
			}
		})
	}
}

func (a *App) connectByID(id string) {
	for _, p := range a.env.Manager.AllProfiles() {
		if p.ID.String() == id {
			_ = a.env.Manager.Connect(context.Background(), p.ID)
			return
		}
	}
}

// installComponent installs one tool and stores its path.
func (a *App) installComponent(component core.ToolComponent) {
	if a.env.Install == nil {
		return
	}
	a.installLog = ""
	go func() {
		err := a.env.Install.Install(context.Background(), []core.ToolComponent{component},
			func(line string) {
				a.installLog += line
				mygo.RunOnMain(func() { a.shell.Window().Invalidate() })
			},
			func(comp core.ToolComponent, path string) error {
				return a.env.Manager.ConfigureInstalledTool(comp, path)
			})
		if err != nil {
			a.vm.Fail(err)
		} else {
			a.vm.Notice = component.Label() + " 已配置完成"
		}
		mygo.RunOnMain(func() { a.shell.Window().Invalidate() })
	}()
}
