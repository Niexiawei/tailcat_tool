package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/theme"
)

// The colours this app owns on top of Onevra's theme. Everything that needs one
// of them reads it from here, so a palette change is one line.
const (
	accentHex        = "#0052d9"
	accentHoverHex   = "#618dff"
	accentPressedHex = "#003cab"
	// warningHex marks a destructive or cautionary action and a warning badge.
	warningHex = "#e37318"
)

// appTheme is Onevra's theme with this app's palette. It is installed once by
// OpenMainWindow, so every view and every component reads the same colours.
func appTheme(c *ui.Context) *ui.Theme {
	t := theme.Onevra(c)
	t.Accent = ui.Hex(accentHex)
	t.AccentHover = ui.Hex(accentHoverHex)
	t.AccentPressed = ui.Hex(accentPressedHex)
	t.Warning = ui.Hex(warningHex)
	return t
}

// selectedRowTint is the selected sidebar row's fill: the accent, washed out so
// the text keeps its contrast.
func selectedRowTint(t *ui.Theme) ui.Color {
	return t.Accent.Alpha(0.12)
}

// warningButton is a filled warning-coloured button for the action that deletes
// something. It is not ButtonPrimary: red-orange says "this removes data", and
// the accent would read as "this is the thing to do here".
func warningButton(c *ui.Context, icon func() *ui.SVG, label string) ui.Element {
	t := c.Theme()
	size := max(float32(12), t.Rem(.875))
	b := components.Button(c, "", components.ButtonPrimary).Label(label)
	background := t.Warning
	if !b.IsDisabled() {
		switch {
		case b.Pressed():
			background = t.Warning.Mix(t.Text, .16)
		case b.Hovered():
			background = t.Warning.Mix(t.Text, .08)
		}
	}
	b.Background(background).Border(1, background).Gap(t.Space(1)).Children(func() {
		components.Icon(c, icon).Size(size, size)
		components.Text(c, label).SingleLine()
	})
	return b
}
