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
	Manager   *core.Manager
	Locate    core.LocateTool
	Runner    core.Runner
	Install   *core.Installer
	ConfigDir string
	// Problems are the startup failures core.Diagnose found. The window shows
	// them all on its own page before the app's pages.
	Problems []core.Problem
	// OpenDir reveals a directory in the file manager, for the startup page's
	// buttons. Nil hides them.
	OpenDir func(dir string) error
	Demo    bool

	OnShutdown func()

	// History is where the log page reads from.
	History core.HistoryReader
	// RecordAppLog stores the app's own errors and warnings.
	RecordAppLog func(core.AppLogRecord)
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
	vm.Demo = env.Demo
	vm.History = env.History
	vm.RecordAppLog = env.RecordAppLog
	vm.OpenDir = env.OpenDir
	// Startup problems are a page of their own, not a toast: a toast saying
	// the configuration failed is gone in eight seconds and never mentioned
	// the history file that failed with it. They are also logged as warnings:
	// the app runs, just without that piece.
	if len(env.Problems) > 0 {
		vm.StartupOpen = true
		vm.StartupProblems = env.Problems
		for _, problem := range env.Problems {
			vm.recordAppLog(core.LogWarn, problem.Title)
		}
	}
	a := &App{env: env, vm: vm}
	vm.Quit = a.Quit
	return a
}

// Start opens the window, the tray and the redraw loop.
func (a *App) Start(trayIcons TrayIcons) {
	a.shell = a.vm.OpenMainWindow()
	window := a.shell.Window()
	watchRedraws(a.env.Manager, window)

	if tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "tailcat-manager",
		Icon:    trayIcons.forBar(false),
		ToolTip: AppName,
		Menu:    a.vm.TrayMenu(a.Show, a.Quit),
	}); err == nil {
		a.tray = tray
		// The bar's brightness is only known once the icon exists.
		applyTrayIcon(tray, trayIcons)
		tray.OnAppearanceChange(func() { applyTrayIcon(tray, trayIcons) })
		go a.keepTrayFresh()
	} else {
		log.Printf("tray icon unavailable: %v", err)
		a.vm.recordAppLog(core.LogWarn, "托盘图标不可用："+err.Error())
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
			a.vm.Notify(component.Label() + " 已配置完成")
		}
		mygo.RunOnMain(func() { a.shell.Window().Invalidate() })
	}()
}
