// Package compat exists only to fail the build when a third-party API we rely
// on changes shape. It holds no production code: every assertion lives in this
// test file, so it costs nothing in the binary while CI (which runs
// go test ./...) catches an upstream break immediately.
package compat

import (
	"context"
	"net"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/oneo-me/onevra/source/shells/plain"
	"github.com/oneo-me/onevra/source/shells/sidebar"
	"github.com/oneo-me/onevra/source/theme"
	"github.com/tailscale/tailcat"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// mygo: window, tray, menus, dialogs, secrets, clipboard, single instance.
var (
	_                               = mygo.NewTray
	_                               = mygo.NewMenu
	_                               = mygo.Separator
	_                               = mygo.Secrets.Get
	_                               = mygo.Secrets.Set
	_                               = mygo.Secrets.Delete
	_                               = mygo.Secrets.Service
	_                               = mygo.App.RequestSingleInstanceLock
	_                               = mygo.App.OnSecondInstance
	_                               = mygo.App.WhenReady
	_                               = mygo.App.Run
	_                               = mygo.Dialog.Open
	_                               = mygo.Clipboard.Write
	_                               = mygo.PathUserData
	_ func(func())                  = mygo.App.WhenReady
	_ func([]string, string) func() = func(args []string, dir string) func() {
		return mygo.App.OnSecondInstance(func([]string, string) {})
	}
)

// mygo/ui: the native view toolkit, its headless tester and invalidation.
var (
	_ = ui.Column
	_ = ui.Text
	_ = ui.NewTester
	_ = (*ui.Context).Invalidate
	_ = ui.Scroll
	_ = ui.Element.FillWidth
	_ = ui.Element.Grow
	_ = terminal.New
	_ = terminal.View
	_ = ui.NewRouter
	_ = (*ui.Router).Push
	_ = (*ui.Router).View
	_ = ui.SegmentedBase
	_ = ui.SegmentedParts.Segment
	_ = ui.TextAreaBuffer
	_ = ui.NewTextBuffer
	_ = ui.Element.TrackScroll
)

// onevra: shells, theme, components and icons.
var (
	_                   = sidebar.NewShell
	_                   = plain.NewShell
	_                   = theme.Onevra
	_                   = components.TextBox
	_                   = components.Radio[string]
	_                   = components.Accordion
	_                   = (*components.AccordionGroup).Item
	_                   = sidebar.HeaderHeight
	_                   = components.Breadcrumbs
	_                   = components.Switch
	_                   = components.Tabs
	_                   = components.TabsSegmented
	_                   = components.Table
	_                   = components.TableRow
	_                   = components.Tooltip
	_                   = ui.RoleTooltip
	_                   = components.IconButton
	_                   = components.IconButtonPlain
	_ func() *ui.SVG    = lucide.CircleCheck
	_ func() *ui.SVG    = lucide.CircleX
	_ func() *ui.SVG    = lucide.Play
	_ func() *ui.SVG    = lucide.X
	_                   = components.Dialog
	_                   = components.AlertDialog
	_                   = components.Collapsible
	_                   = components.Spinner
	_                   = components.Progress
	_                   = components.ButtonPrimary
	_                   = components.ButtonOutline
	_                   = components.ButtonLink
	_                   = components.NativeSelect[string]
	_                   = (*components.NativeSelectParts).Changed
	_                   = components.TextBoxBuffer
	_                   = components.TextBoxOptions{}.ReadOnly
	_ func() *ui.SVG    = lucide.Search
	_ string            = sidebar.ShellOptions{}.Title
	_ func(*ui.Context) = sidebar.ShellOptions{}.Content
	_ func(*ui.Context) = sidebar.ShellOptions{}.ContentTitle
)

// tailcat: address parsing, identities, and the forward/ping primitives.
var (
	_ func(tailcat.Addr) (tailcat.ConnInfo, error)                         = tailcat.ParseAddr
	_ func() *tailcat.PrivateKey                                           = tailcat.NewPrivateKey
	_ func(net.Conn, net.Conn)                                             = tailcat.ProxyConns
	_ func(tailcat.Addr, context.Context, ...any) (tailcat.Addr, error)    = tailcat.Addr.Resolve
	_ func(*tailcat.ConnInfo, context.Context, ...any) error               = (*tailcat.ConnInfo).Expand
	_ func(*tailcat.Client, context.Context, uint16) (net.Conn, error)     = (*tailcat.Client).DialTCPPort
	_ func(*tailcat.Client, context.Context) (tailcat.PingResult, error)   = (*tailcat.Client).Ping
	_ func(*tailcat.Client, context.Context) (*ipnstate.PingResult, error) = (*tailcat.Client).DiscoPing
	_ func(*tailcat.Client) error                                          = (*tailcat.Client).Close
	_ string                                                               = tailcat.DefaultDERPMapURL
)

// The identity file format shared with the tailcat CLI's --key= flag.
var (
	_ key.NodePrivate            = tailcat.PrivateKey{}.Private
	_ func() key.NodePublic      = key.NodePrivate{}.Public
	_ func() *tailcat.PrivateKey = tailcat.NewPrivateKey
)
