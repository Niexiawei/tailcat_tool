// Package compat exists only to fail the build when a third-party API we rely
// on changes shape. It holds no production code: every assertion lives in this
// test file, so it costs nothing in the binary while CI (which runs
// go test ./...) catches an upstream break immediately.
package compat

import (
	"context"
	"io"
	"net"

	"github.com/avast/apkparser"
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

// mygo/ui tables: every page frames its table with internal/app/table.go.
var (
	_ func(*ui.Context, *ui.ListState, []ui.TableColumn, int, func(int, int)) ui.Element = ui.Table
	_                                                                                    = ui.SortOrder{}.Column
	_                                                                                    = ui.SortOrder{}.Descending
	_                                                                                    = ui.TableColumn{}.Sortable
	_                                                                                    = ui.TableLayout{}.Widths
	_                                                                                    = ui.ListState{}.Sort
	_                                                                                    = ui.ListState{}.SortBy
	_                                                                                    = ui.ListState{}.Columns
	_                                                                                    = ui.ListState{}.Selected
	_                                                                                    = ui.ListState{}.Key
	_                                                                                    = ui.Element.Changed
	_                                                                                    = ui.Element.Submitted
	_                                                                                    = ui.ListState{}.FollowEnd
	_                                                                                    = (*ui.ListState).ScrollToEnd
	_                                                                                    = (*ui.Element).RowClicked
)

// onevra: shells, theme, components and icons.
var (
	_                                                                                                                       = sidebar.NewShell
	_                                                                                                                       = plain.NewShell
	_                                                                                                                       = theme.Onevra
	_                                                                                                                       = components.TextBox
	_                                                                                                                       = components.Radio[string]
	_                                                                                                                       = components.Accordion
	_                                                                                                                       = (*components.AccordionGroup).Item
	_                                                                                                                       = sidebar.HeaderHeight
	_                                                                                                                       = components.Breadcrumbs
	_                                                                                                                       = components.Switch
	_                                                                                                                       = components.Tabs
	_                                                                                                                       = components.TabsSegmented
	_                                                                                                                       = components.Tooltip
	_                                                                                                                       = ui.RoleTooltip
	_                                                                                                                       = components.IconButton
	_                                                                                                                       = components.IconButtonPlain
	_                                                                                                                       = components.Avatar
	_                                                                                                                       = components.AvatarOptions{}.Image
	_                                                                                                                       = components.AvatarNoStatus
	_                                                                                                                       = components.ScrollView
	_                                                                                                                       = components.ScrollViewOptions{}.Axes
	_ func(*ui.Context, float64) ui.Element                                                                                 = components.ProgressRing
	_ func(string, apkparser.ManifestEncoder) (error, error, error)                                                         = apkparser.ParseApk
	_ func(io.ReadSeeker) (*apkparser.ZipReader, error)                                                                     = apkparser.OpenZipReader
	_ func(io.Reader) (*apkparser.ResourceTable, error)                                                                     = apkparser.ParseResourceTable
	_ func(*apkparser.ZipReader, apkparser.ManifestEncoder) (error, error)                                                  = apkparser.ParseApkWithZip
	_ func(*ui.Context, string, components.AvatarOptions) ui.Element                                                        = components.Avatar
	_ func(*ui.Context, components.ScrollViewOptions) ui.Element                                                            = components.ScrollView
	_ func() *ui.SVG                                                                                                        = lucide.CircleCheck
	_ func() *ui.SVG                                                                                                        = lucide.CircleX
	_ func() *ui.SVG                                                                                                        = lucide.Play
	_ func() *ui.SVG                                                                                                        = lucide.X
	_                                                                                                                       = components.Dialog
	_                                                                                                                       = components.AlertDialog
	_                                                                                                                       = components.Collapsible
	_                                                                                                                       = components.Spinner
	_                                                                                                                       = components.Progress
	_                                                                                                                       = components.ButtonPrimary
	_                                                                                                                       = components.ButtonOutline
	_                                                                                                                       = components.ButtonLink
	_                                                                                                                       = components.NativeSelect[string]
	_                                                                                                                       = (*components.NativeSelectParts).Changed
	_                                                                                                                       = components.TextBoxBuffer
	_                                                                                                                       = components.TextBoxOptions{}.ReadOnly
	_                                                                                                                       = components.TextBoxOptions{}.LeadingIcon
	_                                                                                                                       = components.Split
	_                                                                                                                       = components.SplitOptions{}.FirstMin
	_                                                                                                                       = components.Tree
	_                                                                                                                       = components.Badge
	_                                                                                                                       = components.BadgeOptions{}.Tone
	_                                                                                                                       = components.BadgeDanger
	_                                                                                                                       = components.Select[string]
	_                                                                                                                       = (*components.SelectParts[string]).Popup
	_                                                                                                                       = (*components.SelectParts[string]).Item
	_ func() *ui.SVG                                                                                                        = lucide.ScrollText
	_ func() *ui.SVG                                                                                                        = lucide.Trash
	_ func() *ui.SVG                                                                                                        = lucide.RefreshCw
	_                                                                                                                       = components.TreeItemWithLeading
	_                                                                                                                       = components.FileIcon
	_                                                                                                                       = mygo.Dialog.Save
	_                                                                                                                       = mygo.FileFilter{}.Extensions
	_ func(*ui.Context, *float32, func(), func(), ...components.SplitOptions) *components.SplitControl                      = components.Split
	_ func(*ui.Context, string) ui.Element                                                                                  = components.FileIcon
	_ func(*ui.Context, func()) ui.Element                                                                                  = components.Tree
	_ func(*ui.Context, string, *bool, bool, func(*ui.Context, float32), func()) ui.Element                                 = components.TreeItemWithLeading
	_ func(*ui.Context, *components.TreeVirtualizedState[string], []string, func(string) []string, func(string)) ui.Element = components.TreeVirtualized[string]
	_                                                                                                                       = components.TreeVirtualizedState[string]{}.Open
	_                                                                                                                       = (*components.TreeVirtualizedState[string]).Rows
	_ func() *ui.SVG                                                                                                        = lucide.Search
	_ string                                                                                                                = sidebar.ShellOptions{}.Title
	_ func(*ui.Context)                                                                                                     = sidebar.ShellOptions{}.Content
	_ func(*ui.Context)                                                                                                     = sidebar.ShellOptions{}.ContentTitle
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
