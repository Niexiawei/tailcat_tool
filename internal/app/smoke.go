// Package app builds the Tailcat Manager user interface on mygo and onevra.
package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/shells/sidebar"
	"github.com/oneo-me/onevra/source/theme"
)

// AppName is the name the window, the tray and the app menu show.
const AppName = "Tailcat Manager"

// OpenMainWindow creates the single manager window and returns its shell.
func OpenMainWindow() *sidebar.Shell {
	shell := sidebar.NewShell(sidebar.ShellOptions{
		Title: AppName,
		Theme: theme.Onevra,
		Content: func(c *ui.Context) {
			ui.Column(c).Fill().Padding(24).Children(func() {
				components.Text(c, AppName)
			})
		},
	})
	shell.Window().OnClosed(func() { mygo.App.Quit() })
	return shell
}
