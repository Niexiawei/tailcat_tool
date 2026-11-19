package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// fileRoots is the tree's single root: the device's own filesystem. Shortcuts
// to /sdcard, /system and friends used to sit beside it, which put the same
// directory on screen twice — once as a pinned row, once inside / — so the two
// rows shared a key and a state, and the tree claimed they were siblings.
var fileRoots = []string{"/"}

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
	// selected is the row highlighted in the table.
	selected string
	// crumb is what the breadcrumbs wrote when an ancestor was activated.
	crumb int
	// tree owns expansion, the cursor and the scroll offset. Keys are device
	// paths, which are unique; labels are not, and two folders called DCIM
	// would otherwise share one row's state.
	tree components.TreeVirtualizedState[string]
	// open is last frame's expansion set, so the page can tell which rows the
	// user folded and forget what those rows were hiding.
	open  map[string]bool
	table ui.ListState
	// chosen is the row the table highlights, and chosenPath is the directory
	// its index belongs to. They live here rather than in the build pass
	// because the table writes the choice between frames, as the arrow keys
	// move it; recomputing it from selected every frame would undo that.
	chosen     int
	chosenPath string
}

// fileStateFor returns one device's page state, creating it on first use.
func (vm *ViewModel) fileStateFor(device uuid.UUID) *fileState {
	if vm.files == nil {
		vm.files = map[uuid.UUID]*fileState{}
	}
	state, ok := vm.files[device]
	if !ok {
		state = &fileState{treeWidth: 240, path: "/sdcard", selected: "/sdcard", crumb: -1}
		// Open the way down to the folder the page starts on, so the tree shows
		// where that folder lives instead of a single closed row.
		openTreeTo(state, state.path)
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

// fileLoading reports whether one directory is being read right now.
func (vm *ViewModel) fileLoading(key string) bool {
	vm.fileMu.Lock()
	defer vm.fileMu.Unlock()
	return vm.fileLoads[key]
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
	key := fileKey(profile.ID, path)
	if !force {
		if _, ok := vm.Manager.DirListing(profile.ID, path); ok {
			return
		}
		// A folder that failed stays failed until the user asks again. Retrying
		// on every frame would clear the reason, start another read and leave
		// the page on 「正在读取…」 forever, which is what made an unreadable
		// folder look like a hang.
		if vm.fileFailure(key) != "" {
			return
		}
	}
	if !vm.beginFileLoad(key) {
		return
	}
	vm.clearFileFailure(key)
	go func() {
		listing, err := vm.Manager.ListRemoteDir(context.Background(), profile.ID, path)
		// A cancelled read means the device went away mid-listing, which the
		// page already says with its own state; it is not a failure to report.
		if errors.Is(err, core.ErrCancelled) {
			err = nil
		}
		vm.endFileLoad(key, err)
		if err == nil {
			// Read the next level while the user is still looking at this one,
			// so opening a folder he is about to click costs no wait.
			vm.prefetchDirs(profile, listing)
		}
	}()
}

// prefetchSkip are the pseudo filesystems a file manager has no use for: their
// listings are huge, change constantly and cannot be opened as files.
var prefetchSkip = map[string]bool{"/proc": true, "/sys": true, "/dev": true, "/acct": true}

// prefetchParallel is the ceiling on read-ahead at once. Eight shell round trips
// fill a tree in a fraction of the sequential time without burying the device's
// shell (and adb server) under a hundred. A read the user asked for never waits
// behind these.
const prefetchParallel = 8

// prefetchSlots keeps the read-ahead under that ceiling across the whole app:
// the adb server is one process, and eight shell round trips at a time is what
// it handles without making every one of them slower. The semaphore lives here
// rather than on the page state because two devices browsing at once share it.
var prefetchSlots = make(chan struct{}, prefetchParallel)

// prefetchDirs reads the subdirectories of a listing that just arrived, so
// expanding the next level shows folders instead of 「正在读取…」. The reads run
// eight at a time: one shell round trip after another made a folder of forty
// subfolders wait for forty of them before its arrows appeared.
func (vm *ViewModel) prefetchDirs(profile core.Profile, listing core.DirListing) {
	var wg sync.WaitGroup
	for _, entry := range listing.Entries {
		if !entry.Dir || prefetchSkip[entry.Path] {
			continue
		}
		// A folder that is already cached needs no slot at all; the checks in
		// prefetchDir cover the ones that land while this one waits its turn.
		if _, ok := vm.Manager.DirListing(profile.ID, entry.Path); ok {
			continue
		}
		path := entry.Path
		wg.Go(func() {
			prefetchSlots <- struct{}{}
			defer func() { <-prefetchSlots }()
			vm.prefetchDir(profile, path)
		})
	}
	wg.Wait()
}

// prefetchDir reads one folder for the tree's arrows. A folder already cached,
// already in flight, or already known to fail is left alone, so a hundred queued
// reads cannot turn into a hundred shell commands.
func (vm *ViewModel) prefetchDir(profile core.Profile, path string) {
	key := fileKey(profile.ID, path)
	if _, ok := vm.Manager.DirListing(profile.ID, path); ok {
		return
	}
	if vm.fileFailure(key) != "" || !vm.beginFileLoad(key) {
		return
	}
	_, err := vm.Manager.ListRemoteDir(context.Background(), profile.ID, path)
	// A cancelled read means the device went away mid-listing, which the page
	// already says with its own state; it is not a failure to report.
	if errors.Is(err, core.ErrCancelled) {
		err = nil
	}
	vm.endFileLoad(key, err)
}

// showDir points the right pane at a directory, and opens the tree down to it:
// a selection the tree cannot show is a selection the user cannot find again.
func (vm *ViewModel) showDir(profile core.Profile, state *fileState, dir string) {
	state.path = dir
	state.selected = dir
	state.crumb = -1
	openTreeTo(state, dir)
	vm.ensureDirs(profile, dir)
}

// openTreeTo opens every row from the root down to dir.
//
// The root is part of the path, so it opens too. Leaving it out is what made
// the two panes stop agreeing: once / was collapsed by hand, the table could
// walk all the way down to /sdcard/Android/data while the tree stayed on one
// collapsed row.
func openTreeTo(state *fileState, dir string) {
	state.tree.Open.Add("/")
	for path := dir; path != "" && path != "/"; path = core.RemoteParent(path) {
		state.tree.Open.Add(path)
	}
}

// revealInTree puts the tree's highlight and scroll on the folder the right
// pane shows. The table is the source of truth: without this the tree keeps its
// cursor on whichever row was chosen first — usually / — and the highlight
// contradicts the breadcrumbs. It runs after the rows are built for the frame,
// so the target row is known. Setting the cursor directly, instead of choosing
// it, keeps OnChange from navigating back where the user just came from.
func revealInTree(state *fileState) {
	if state.tree.List.Selected == nil {
		return
	}
	row := -1
	for i := 0; i < state.tree.Rows(); i++ {
		if state.tree.Item(i) == state.path {
			row = i
			break
		}
	}
	if row < 0 || row == *state.tree.List.Selected {
		return
	}
	*state.tree.List.Selected = row
	state.tree.List.ScrollIntoView(row)
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
	theme := c.Theme()
	// The whole browser sits in one frame, the way the console does: a file
	// manager without an edge bleeds into the page and the two panes look like
	// two unrelated columns. The 1 DIP padding keeps the panes off the border.
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(1).
		Radius(theme.Radius).Border(1, theme.Border).Clip().Children(func() {
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
//
// The tree is virtualized and keyed by path: every row is a direct child of the
// tree's column, so a label-keyed tree cannot tell /sdcard/DCIM from
// /system/DCIM and would give the two rows one shared state.
func (vm *ViewModel) folderTree(c *ui.Context, profile core.Profile, state *fileState) {
	forgetFoldedSubtrees(state)
	children := func(path string) []string {
		folders, known := vm.subfolders(profile, path)
		if !known && state.tree.Open.Has(path) {
			// Nothing read yet and the user wants in: read it now.
			vm.ensureDirs(profile, path)
		}
		return treeChildren(folders, known)
	}
	state.tree.List.OnChange = func() { vm.followTreeCursor(profile, state) }
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(6, 4).Children(func() {
		tree := components.TreeVirtualized(c, &state.tree, fileRoots, children, func(path string) {
			vm.folderRow(c, profile, state, path)
		})
		// The rows exist now, so the highlight can be moved onto the folder the
		// right pane is showing. Building the tree does not do it by itself.
		revealInTree(state)
		tree.FillWidth().Grow(1).MinHeight(0)
	})
}

// forgetFoldedSubtrees drops the expansion flags a folded branch left behind.
// A closed row hides everything under it, so the flags underneath have to go
// with it: keeping them meant folding / and opening it again brought /sdcard
// back expanded — a subtree the user had just folded away, and one the page
// never asked to open (「只要展开 / 就会自动选中 /sdcard 并且展开」).
func forgetFoldedSubtrees(state *fileState) {
	open := make(map[string]bool, state.tree.Open.Len())
	for path := range state.tree.Open.All() {
		open[path] = true
	}
	for path := range state.open {
		if open[path] {
			continue
		}
		// path was folded between frames: it is hiding everything below it.
		for hidden := range open {
			if remoteUnder(hidden, path) {
				delete(open, hidden)
				state.tree.Open.Remove(hidden)
			}
		}
	}
	state.open = open
}

// remoteUnder reports whether path lives inside dir rather than beside it.
func remoteUnder(path, dir string) bool {
	if dir == "/" {
		return path != "/" && strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, dir+"/")
}

// treeChildren answers what a tree row can expand into. The component shows a
// disclosure arrow exactly when the result is non-nil, so this is also the rule
// for "is there anything inside": a folder nobody has read yet keeps the arrow
// (it may well have something in it), and a folder known to hold nothing gets
// no arrow at all — an arrow that opens onto nothing is a promise the folder
// cannot keep.
func treeChildren(folders []string, known bool) []string {
	switch {
	case !known:
		return []string{}
	case len(folders) > 0:
		return folders
	default:
		return nil
	}
}

// subfolders lists the subdirectories of one directory, and whether it has been
// read at all.
func (vm *ViewModel) subfolders(profile core.Profile, path string) ([]string, bool) {
	listing, ok := vm.Manager.DirListing(profile.ID, path)
	if !ok {
		return nil, false
	}
	folders := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		if entry.Folder() {
			folders = append(folders, entry.Path)
		}
	}
	return folders, true
}

// followTreeCursor points the right pane at the row the user just picked.
func (vm *ViewModel) followTreeCursor(profile core.Profile, state *fileState) {
	if state.tree.List.Selected == nil {
		return
	}
	row := *state.tree.List.Selected
	if row < 0 || row >= state.tree.Rows() {
		return
	}
	vm.showDir(profile, state, state.tree.Item(row))
}

// folderRow is one tree row's content, after the component's own arrow: a
// folder mark, the name, and — when there is one — why it is not open yet.
// Indentation belongs to the component, so nothing here measures depth.
func (vm *ViewModel) folderRow(c *ui.Context, profile core.Profile, state *fileState, path string) {
	theme := c.Theme()
	label := core.RemoteBase(path)
	components.Icon(c, lucide.Folder()).Size(14, 14)
	name := components.Text(c, label).FontSize(12).SingleLine()
	components.Tooltip(c, name, path)
	key := fileKey(profile.ID, path)
	if reason := vm.fileFailure(key); reason != "" {
		badge := components.Text(c, "读取失败").FontSize(10).TextColor(theme.Danger).SingleLine()
		components.Tooltip(c, badge, reason)
		return
	}
	if vm.fileLoading(key) {
		components.Spinner(c, components.SpinnerRing).Width(12).Height(12).Label("正在读取文件夹")
	}
}

// fileTable is the right pane: the entries of the selected directory.
func (vm *ViewModel) fileTable(c *ui.Context, profile core.Profile, state *fileState) {
	theme := c.Theme()
	key := fileKey(profile.ID, state.path)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(6).Padding(6, 4, 6, 6).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			// The path is a breadcrumb: every ancestor is a way back, which is
			// what a deep tree needs instead of scrolling up to find the parent.
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				trail := remoteTrail(state.path)
				if components.Breadcrumbs(c, trail, &state.crumb).Changed() {
					vm.showDir(profile, state, remoteTrailPath(trail, state.crumb))
				}
			})
			if actionButton(c, lucide.RefreshCw, "刷新", components.ButtonSecondary).Clicked() {
				vm.reloadDir(profile, state.path)
			}
			if actionButton(c, lucide.Upload, "上传到此文件夹…", components.ButtonSecondary).Clicked() {
				vm.pushInto(profile, state.path)
			}
		})

		listing, ok := vm.Manager.DirListing(profile.ID, state.path)
		if !ok {
			if reason := vm.fileFailure(key); reason != "" {
				// Centered for the same reason the spinner is: this is the whole
				// content of the pane, not a line of it.
				ui.Column(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Center).
					Justify(ui.Center).Gap(8).Children(func() {
					components.Text(c, "读取失败："+reason).FontSize(11).TextColor(theme.Danger).SingleLine()
					if components.Button(c, "重试", components.ButtonSecondary).Clicked() {
						vm.clearFileFailure(key)
						vm.loadDir(profile, state.path, true)
					}
				})
				return
			}
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Center).
				Justify(ui.Center).Gap(8).Children(func() {
				components.Spinner(c, components.SpinnerRing).Width(18).Height(18).Label("正在读取文件夹")
				components.Text(c, "正在读取…").FontSize(11).TextColor(theme.TextMuted)
			})
			return
		}
		if listing.Truncated {
			components.Text(c, "只显示前 2000 项，请进入子文件夹查看其余内容").FontSize(11).TextColor(theme.Danger)
		}
		if len(listing.Entries) == 0 {
			components.Text(c, "这个文件夹是空的").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			return
		}
		// The chosen row is the path the page selected: a click on another row
		// reports Changed, which is what opens it.
		if state.chosenPath != state.path {
			// A new directory: the highlight starts on the row the page
			// selected, if this listing holds it.
			state.chosenPath = state.path
			state.chosen = -1
			for i, entry := range listing.Entries {
				if entry.Path == state.selected {
					state.chosen = i
					break
				}
			}
		}
		state.table.Selected = &state.chosen
		// A click opens the entry; the arrow keys only move the highlight, as a
		// file manager's do. Changed reports both, RowClicked only the
		// pointer's click.
		opened := -1
		tableCard(c, &state.table, fileColumns(), len(listing.Entries), tableOptions{
			// The pane is already inside the browser's 1 DIP frame.
			Label: "设备文件列表", Grow: true, Unframed: true,
		}, func(row, col int) {
			entry := listing.Entries[row]
			cell := ui.Row(c).FillWidth().MinWidth(0).Children(func() {
				vm.fileCell(c, col, entry)
			})
			if cell.RowClicked() && opened < 0 {
				opened = row
			}
		})
		if opened >= 0 && opened < len(listing.Entries) {
			vm.activateEntry(profile, state, listing.Entries[opened])
		}
	})
}

// remoteTrail splits a device path into breadcrumb items, root first.
func remoteTrail(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{"/"}
	}
	return append([]string{"/"}, strings.Split(trimmed, "/")...)
}

// remoteTrailPath is the directory an activated breadcrumb item points at.
func remoteTrailPath(trail []string, index int) string {
	if index <= 0 || index >= len(trail) {
		return "/"
	}
	return "/" + strings.Join(trail[1:index+1], "/")
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
	if entry.Folder() {
		vm.showDir(profile, state, entry.Path)
		return
	}
	vm.FileDetail = &FileDetail{Device: profile.ID, Entry: entry}
}

// fileSizeLabel reports a size the way a file manager does: a folder's own
// bookkeeping is not a size the user asked for.
func fileSizeLabel(entry core.RemoteEntry) string {
	// A folder's number is filesystem bookkeeping and a link's is the length of
	// its target path; neither is a size the user asked for.
	if entry.Folder() || entry.Size < 0 {
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
