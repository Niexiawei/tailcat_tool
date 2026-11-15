package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/shells/sidebar"
	"github.com/oneo-me/onevra/source/theme"
)

// AppName is what the window, the tray and the app menu show.
const AppName = "Tailcat Manager"

// OpenMainWindow creates the manager window and returns its shell.
func (vm *ViewModel) OpenMainWindow() *sidebar.Shell {
	shell := sidebar.NewShell(sidebar.ShellOptions{
		Title:    AppName,
		Theme:    theme.Onevra,
		StateKey: "tailcat-manager.main",
		// Left is the scrolling device list, LeftToolbar the fixed footer:
		// a footer inside Left competes with the list for height.
		Left:        vm.SidebarView,
		LeftToolbar: vm.SidebarFooter,
		Content: func(c *ui.Context) {
			vm.ContentView(c)
		},
	})
	window := shell.Window()
	vm.Invalidate = window.Invalidate
	// Closing the window keeps the channels running; the tray reopens it.
	window.OnClosed(func() { window.Hide() })
	return shell
}

// watchRedraws repaints the window whenever the manager reports a change.
func watchRedraws(m interface{ Changed() <-chan struct{} }, window *mygo.Window) {
	go func() {
		for range m.Changed() {
			window.Invalidate()
		}
	}()
}
