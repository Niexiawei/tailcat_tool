package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// fileRoots are the entry points the folder tree always offers. They are the
// five directories every Android install has; anything else is reached by
// descending from one of them.
var fileRoots = []string{"/sdcard", "/storage/emulated/0", "/data/local/tmp", "/system", "/"}

// FileDetail is the entry whose attribute dialog is open; nil closes it.
type FileDetail struct {
	Device uuid.UUID
	Entry  core.RemoteEntry
}

// Local pickers are variables so tests never open a real dialog.
var (
	openLocalPaths = mygo.Dialog.Open
	saveLocalPath  = mygo.Dialog.Save
)

// pullTimeout bounds one download; a folder of photos through a relay is not
// fast, and the task stays cancellable meanwhile.
const pullTimeout = 30 * time.Minute

// fileState is one device's file page state. Everything that is not device data
// lives here, so the page stays a function of state.
type fileState struct {
	// treeWidth is the first pane of the split, in DIPs.
	treeWidth float32
	// path is the directory the right pane lists.
	path string
	// selected is the row highlighted in both panes.
	selected string
	// open is per tree node expansion. TreeItemWithLeading wants a *bool, and
	// a map's elements are not addressable, so each node keeps its own pointer.
	open  map[string]*bool
	table components.TableState
}

// openAt returns the node's expansion pointer, creating it on first use.
func (s *fileState) openAt(path string) *bool {
	if s.open == nil {
		s.open = map[string]*bool{}
	}
	value, ok := s.open[path]
	if !ok {
		value = new(bool)
		s.open[path] = value
	}
	return value
}

// fileStateFor returns one device's page state, creating it on first use.
func (vm *ViewModel) fileStateFor(device uuid.UUID) *fileState {
	if vm.files == nil {
		vm.files = map[uuid.UUID]*fileState{}
	}
	state, ok := vm.files[device]
	if !ok {
		state = &fileState{treeWidth: 240, path: "/sdcard", selected: "/sdcard"}
		vm.files[device] = state
	}
	return state
}

// fileKey names one directory read, for the in-flight and failure maps.
func fileKey(device uuid.UUID, path string) string {
	return device.String() + "|" + path
}

// beginFileLoad claims one directory read, reporting false when one is already
// running: expanding a node twice must not send the command twice.
func (vm *ViewModel) beginFileLoad(key string) bool {
	vm.fileMu.Lock()
	defer vm.fileMu.Unlock()
	if vm.fileLoads[key] {
		return false
	}
	if vm.fileLoads == nil {
		vm.fileLoads = map[string]bool{}
	}
	vm.fileLoads[key] = true
	return true
}

// endFileLoad records the outcome where the page can show it, and repaints.
func (vm *ViewModel) endFileLoad(key string, err error) {
	vm.fileMu.Lock()
	delete(vm.fileLoads, key)
	if err != nil {
		if vm.fileErrs == nil {
			vm.fileErrs = map[string]string{}
		}
		vm.fileErrs[key] = err.Error()
	} else {
		delete(vm.fileErrs, key)
	}
	vm.fileMu.Unlock()
	if vm.Invalidate != nil {
		vm.Invalidate()
	}
}

// fileFailure is the reason one directory could not be read, if it could not.
func (vm *ViewModel) fileFailure(key string) string {
	vm.fileMu.Lock()
	defer vm.fileMu.Unlock()
	return vm.fileErrs[key]
}

func (vm *ViewModel) clearFileFailure(key string) {
	vm.fileMu.Lock()
	delete(vm.fileErrs, key)
	vm.fileMu.Unlock()
}

// ensureDirs reads the directories that are not cached yet. It never blocks:
// each read runs off the render path and notifies when it lands.
func (vm *ViewModel) ensureDirs(profile core.Profile, paths ...string) {
	for _, path := range paths {
		vm.loadDir(profile, path, false)
	}
}

// reloadDir reads one directory again, replacing what is cached for it.
func (vm *ViewModel) reloadDir(profile core.Profile, path string) {
	vm.loadDir(profile, path, true)
}

func (vm *ViewModel) loadDir(profile core.Profile, path string, force bool) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return
	}
	if !force {
		if _, ok := vm.Manager.DirListing(profile.ID, path); ok {
			return
		}
	}
	key := fileKey(profile.ID, path)
	if !vm.beginFileLoad(key) {
		return
	}
	vm.clearFileFailure(key)
	go func() {
		_, err := vm.Manager.ListRemoteDir(context.Background(), profile.ID, path)
		// A cancelled read means the device went away mid-listing, which the
		// page already says with its own state; it is not a failure to report.
		if errors.Is(err, core.ErrCancelled) {
			err = nil
		}
		vm.endFileLoad(key, err)
	}()
}

// showDir points the right pane at a directory; the tree follows it.
func (vm *ViewModel) showDir(profile core.Profile, state *fileState, dir string) {
	state.path = dir
	state.selected = dir
	*state.openAt(dir) = true
	vm.ensureDirs(profile, dir)
}

// filesPage draws the file tab: the folder tree on the left, the entries of the
// selected folder on the right.
func (vm *ViewModel) filesPage(c *ui.Context, profile core.Profile, session core.Session) {
	if session.State != core.StateOnline {
		vm.filesOffline(c, session)
		return
	}
	state := vm.fileStateFor(profile.ID)
	vm.ensureDirs(profile, state.path)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		components.Split(c, &state.treeWidth,
			func() { vm.folderTree(c, profile, state) },
			func() { vm.fileTable(c, profile, state) },
			components.SplitOptions{FirstMin: 180, SecondMin: 260, Default: 260},
		).Fill().Label("文件夹树与文件列表")
	})
}

// filesOffline says why there is nothing to browse instead of drawing an empty
// table, which would read as an empty device.
func (vm *ViewModel) filesOffline(c *ui.Context, session core.Session) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Padding(0, 2).Gap(4).Children(func() {
		components.Text(c, "连接后才能浏览设备文件。").FontSize(11).TextColor(theme.TextMuted)
		if session.Err != "" {
			components.Text(c, session.Err).FontSize(11).TextColor(theme.Danger)
		}
	})
}

// folderTree is the left pane: one root per entry point, expanded on demand.
func (vm *ViewModel) folderTree(c *ui.Context, profile core.Profile, state *fileState) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(0, 4).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			components.Tree(c, func() {
				for _, root := range fileRoots {
					// A root is named by its whole path: its last element is
					// often just "0" or "/".
					vm.folderNode(c, profile, state, root, root)
				}
			})
		})
	})
}

// folderNode draws one directory in the tree. children is nil while the node is
// collapsed, which is what makes the tree lazy.
func (vm *ViewModel) folderNode(c *ui.Context, profile core.Profile, state *fileState, path, label string) {
	open := state.openAt(path)
	var children func()
	if *open {
		children = func() { vm.folderChildren(c, profile, state, path) }
	}
	row := components.TreeItemWithLeading(c, label, open, state.selected == path,
		func(c *ui.Context, size float32) { components.Icon(c, lucide.Folder()).Size(size, size) },
		children)
	if row.Clicked() {
		vm.showDir(profile, state, path)
	}
}

// folderChildren lists the subdirectories of an expanded node.
func (vm *ViewModel) folderChildren(c *ui.Context, profile core.Profile, state *fileState, path string) {
	theme := c.Theme()
	key := fileKey(profile.ID, path)
	listing, ok := vm.Manager.DirListing(profile.ID, path)
	if !ok {
		if reason := vm.fileFailure(key); reason != "" {
			ui.Column(c).Gap(4).Padding(0, 0, 0, 6).Children(func() {
				components.Text(c, "读取失败："+reason).FontSize(11).TextColor(theme.Danger).SingleLine()
				if components.Button(c, "重试", components.ButtonGhost).Clicked() {
					vm.clearFileFailure(key)
					vm.loadDir(profile, path, true)
				}
			})
			return
		}
		components.Text(c, "正在读取…").FontSize(11).TextColor(theme.TextMuted)
		return
	}
	folders := 0
	for _, entry := range listing.Entries {
		if !entry.Dir {
			continue
		}
		folders++
		vm.folderNode(c, profile, state, entry.Path, entry.Name)
	}
	if folders == 0 {
		components.Text(c, "没有子文件夹").FontSize(11).TextColor(theme.TextMuted)
	}
}

// fileTable is the right pane: the entries of the selected directory.
func (vm *ViewModel) fileTable(c *ui.Context, profile core.Profile, state *fileState) {
	theme := c.Theme()
	key := fileKey(profile.ID, state.path)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(6).Padding(4, 0, 4, 4).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			pathText := components.Text(c, state.path).FontSize(11).SingleLine().Grow(1).MinWidth(0)
			components.Tooltip(c, pathText, state.path)
			if actionButton(c, lucide.RefreshCw, "刷新", components.ButtonGhost).Clicked() {
				vm.reloadDir(profile, state.path)
			}
			if actionButton(c, lucide.Upload, "上传到此文件夹…", components.ButtonSecondary).Clicked() {
				vm.pushInto(profile, state.path)
			}
		})

		listing, ok := vm.Manager.DirListing(profile.ID, state.path)
		if !ok {
			if reason := vm.fileFailure(key); reason != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					components.Text(c, "读取失败："+reason).FontSize(11).TextColor(theme.Danger).SingleLine()
					if components.Button(c, "重试", components.ButtonGhost).Clicked() {
						vm.clearFileFailure(key)
						vm.loadDir(profile, state.path, true)
					}
				})
				return
			}
			components.Text(c, "正在读取…").FontSize(11).TextColor(theme.TextMuted)
			return
		}
		if listing.Truncated {
			components.Text(c, "只显示前 2000 项，请进入子文件夹查看其余内容").FontSize(11).TextColor(theme.Danger)
		}
		components.ScrollView(c, components.ScrollViewOptions{}).FillWidth().Grow(1).MinHeight(0).
			Label("设备文件列表").Children(func() {
			if len(listing.Entries) == 0 {
				components.Text(c, "这个文件夹是空的").FontSize(11).TextColor(theme.TextMuted)
				return
			}
			components.Table(c, &state.table, fileColumns(), func() {
				for _, entry := range listing.Entries {
					entry := entry
					row := components.TableRow(c, entry.Path, entry.Name, state.selected == entry.Path, func(col int) {
						vm.fileCell(c, col, entry)
					})
					if row.Clicked() {
						vm.activateEntry(profile, state, entry)
					}
				}
			})
		})
	})
}

// fileColumns are the file table's columns, in display order.
func fileColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "名称", ID: "name", MinWidth: 180},
		{Title: "大小", ID: "size", Width: 96, Align: ui.End},
		{Title: "修改时间", ID: "modified", Width: 140},
		{Title: "权限", ID: "mode", Width: 110},
	}
}

func (vm *ViewModel) fileCell(c *ui.Context, col int, entry core.RemoteEntry) {
	theme := c.Theme()
	switch col {
	case 0:
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			if entry.Dir {
				components.Icon(c, lucide.Folder()).Size(14, 14)
			} else {
				components.FileIcon(c, entry.Name).Size(14, 14)
			}
			name := components.Text(c, entry.Name).FontSize(12).SingleLine()
			components.Tooltip(c, name, entry.Name)
			if entry.Link {
				components.Text(c, "链接").FontSize(10).TextColor(theme.TextMuted)
			}
		})
	case 1:
		components.Text(c, fileSizeLabel(entry)).FontSize(12).SingleLine()
	case 2:
		components.Text(c, formatRemoteTime(entry.Modified)).FontSize(12).SingleLine()
	default:
		components.Text(c, entry.Mode).FontSize(12).SingleLine()
	}
}

// activateEntry is what clicking a row does: a folder opens, a file shows its
// attributes and the two transfers.
func (vm *ViewModel) activateEntry(profile core.Profile, state *fileState, entry core.RemoteEntry) {
	state.selected = entry.Path
	if entry.Dir {
		vm.showDir(profile, state, entry.Path)
		return
	}
	vm.FileDetail = &FileDetail{Device: profile.ID, Entry: entry}
}

// fileSizeLabel reports a size the way a file manager does: a folder's own
// bookkeeping is not a size the user asked for.
func fileSizeLabel(entry core.RemoteEntry) string {
	if entry.Dir || entry.Size < 0 {
		return "—"
	}
	return core.FormatBytes(uint64(entry.Size))
}

// formatRemoteTime renders a device timestamp; a zero means the device did not
// report one.
func formatRemoteTime(at time.Time) string {
	if at.IsZero() {
		return "—"
	}
	return at.Local().Format("2006-01-02 15:04")
}

// uploadTarget is the directory a push from this entry belongs in.
func uploadTarget(entry core.RemoteEntry) string {
	if entry.Dir {
		return entry.Path
	}
	return core.RemoteParent(entry.Path)
}

// pushInto uploads the local files the user picks into remoteDir.
func (vm *ViewModel) pushInto(profile core.Profile, remoteDir string) {
	if err := ValidateRemotePath(remoteDir); err != nil {
		vm.Fail(err)
		return
	}
	paths, err := openLocalPaths(mygo.OpenDialogOptions{
		Title:    "选择要上传到「" + remoteDir + "」的文件",
		Multiple: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	for _, local := range paths {
		if local == "" {
			continue
		}
		if _, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "上传 "+fileName(local),
			[]string{"push", local, remoteDir}, pullTimeout); err != nil {
			vm.Fail(err)
			return
		}
	}
}

// pullEntry downloads one entry: a save dialog for a file, a folder picker for
// a directory.
func (vm *ViewModel) pullEntry(profile core.Profile, entry core.RemoteEntry) {
	if entry.Dir {
		paths, err := openLocalPaths(mygo.OpenDialogOptions{
			Title:     "选择把「" + entry.Name + "」保存到哪个目录",
			Directory: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		vm.startPull(profile, entry, paths[0])
		return
	}
	target, err := saveLocalPath(mygo.SaveDialogOptions{
		Title:       "保存「" + entry.Name + "」到本地",
		DefaultPath: entry.Name,
	})
	if err != nil || target == "" {
		return
	}
	vm.startPull(profile, entry, target)
}

// startPull starts the download task.
func (vm *ViewModel) startPull(profile core.Profile, entry core.RemoteEntry, local string) {
	if err := ValidateRemotePath(entry.Path); err != nil {
		vm.Fail(err)
		return
	}
	if _, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "下载 "+entry.Name,
		[]string{"pull", entry.Path, local}, pullTimeout); err != nil {
		vm.Fail(err)
	}
}

// FileDetailView draws the entry dialog: the attributes Android can report, and
// the two transfers this page exists for.
func (vm *ViewModel) FileDetailView(c *ui.Context) {
	if vm.FileDetail == nil {
		return
	}
	theme := c.Theme()
	detail := *vm.FileDetail
	open := true
	components.Dialog(c, &open, components.DialogOptions{Label: "文件属性"}, func(panel ui.Element) {
		panel.Padding(18).Gap(10).Width(460)
		panel.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				name := components.Text(c, detail.Entry.Name).FontSize(18).Bold().SingleLine()
				components.Tooltip(c, name, detail.Entry.Name)
				if components.IconButton(c, lucide.X(), "关闭", components.IconButtonPlain).Clicked() {
					open = false
				}
			})
			pathText := components.Text(c, detail.Entry.Path).FontSize(11).TextColor(theme.TextMuted).SingleLine()
			components.Tooltip(c, pathText, detail.Entry.Path)

			// No creation time: Android's filesystems rarely keep one, and a
			// row that always says 「—」 is worse than a row that is absent.
			detailRow(c, "文件名", detail.Entry.Name)
			detailRow(c, "文件大小", fileSizeLabel(detail.Entry))
			detailRow(c, "最后修改时间", formatRemoteTime(detail.Entry.Modified))
			detailRow(c, "文件权限", detail.Entry.Mode+" ("+detail.Entry.Octal+")")

			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				label := "下载到本地…"
				if detail.Entry.Dir {
					label = "下载此文件夹…"
				}
				if actionButton(c, lucide.Download, label, components.ButtonPrimary).Clicked() {
					if profile, ok := vm.profileByID(detail.Device.String()); ok {
						vm.pullEntry(profile, detail.Entry)
					}
				}
				if actionButton(c, lucide.Upload, "上传到此文件夹…", components.ButtonSecondary).Clicked() {
					if profile, ok := vm.profileByID(detail.Device.String()); ok {
						vm.pushInto(profile, uploadTarget(detail.Entry))
					}
				}
			})
		})
	})
	// Esc, the scrim and the ✕ all land here, so closing has one exit.
	if !open {
		vm.FileDetail = nil
	}
}
