package app

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The fixtures the fake device prints, one per directory.
const (
	sdcardListing = "directory|4096|770|drwxrwx---|1700000000|./DCIM\n" +
		"directory|4096|770|drwxrwx---|1700000000|./Download\n" +
		"regular file|2048|660|-rw-rw----|1700000000|./log.txt\n" +
		"regular file|2411724|660|-rw-rw----|1700000000|./照片.jpg\n"
	systemListing = "directory|4096|755|drwxr-xr-x|1700000000|./bin\n" +
		"regular file|4096|644|-rw-r--r--|1700000000|./build.prop\n"
	downloadListing = "regular file|52428800|660|-rw-rw----|1700000000|./update.zip\n"
)

// dirOfScript recovers the directory a listing script was built for.
func dirOfScript(script string) (string, bool) {
	const prefix = "cd '"
	if !strings.HasPrefix(script, prefix) {
		return "", false
	}
	rest := script[len(prefix):]
	end := strings.Index(rest, "' 2>&1 || exit 9")
	if end < 0 {
		end = strings.Index(rest, "' || exit 9")
	}
	if end < 0 {
		return "", false
	}
	return strings.ReplaceAll(rest[:end], `'\''`, "'"), true
}

// readFixture answers every directory the default fixtures describe. / holds a
// symlinked sdcard, like a real device, so the tree has to follow links.
func readFixture(dir string) (core.Result, error) {
	switch dir {
	case "/":
		return core.Result{Output: `symbolic link|4|777|lrwxrwxrwx|1700000000|./sdcard
directory|4096|755|drwxr-xr-x|1700000000|./system
directory|4096|755|drwxr-xr-x|1700000000|./storage
`}, nil
	case "/sdcard":
		return core.Result{Output: sdcardListing}, nil
	case "/system":
		return core.Result{Output: systemListing}, nil
	case "/sdcard/Download":
		return core.Result{Output: downloadListing}, nil
	}
	return core.Result{}, nil
}

// newConnectedFilesViewModel builds a connected device whose directory reads are
// answered by read, positioned on the file page and starting wherever a fresh
// connection starts.
func newConnectedFilesViewModel(t *testing.T, read func(dir string) (core.Result, error)) (*ViewModel, func() []string) {
	t.Helper()
	vm, commands := newRunnerViewModel(t, func(c core.Command) (core.Result, error) {
		for _, arg := range c.Args {
			if arg == "get-state" {
				return core.Result{Output: "device\n"}, nil
			}
		}
		if dir, ok := dirOfScript(c.Args[len(c.Args)-1]); ok {
			return read(dir)
		}
		return core.Result{}, nil
	})
	t.Cleanup(func() { vm.Manager.Shutdown(context.Background()) })
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	vm.DetailTab = detailFiles
	vm.OpenDevice(profile.ID)
	return vm, commands
}

// newFilesViewModel is the same device parked on /sdcard: most cases here are
// about what the listing shows, not about where a connection starts.
func newFilesViewModel(t *testing.T, read func(dir string) (core.Result, error)) (*ViewModel, func() []string) {
	t.Helper()
	vm, commands := newConnectedFilesViewModel(t, read)
	profile := vm.Manager.Profiles()[0]
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard")
	return vm, commands
}

// waitForText frames until the text shows up. Directory reads run off the
// render path, so a test has to let them land.
func waitForText(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tt.Frame()
		if tt.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q; texts = %v", want, tt.Texts())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestFilePageListsFoldersAndFiles(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	for _, want := range []string{"Download", "log.txt", "照片.jpg", "名称", "大小", "修改时间", "权限", "2.3 MB", "-rw-rw----"} {
		if !tt.HasText(want) {
			t.Fatalf("the file page has no %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestFilePageHasNoCreationTime(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	if tt.HasText("创建时间") {
		t.Fatalf("Android keeps no birth time, so the column must not exist; texts = %v", tt.Texts())
	}
}

func TestFilePageExplainsAnOfflineDevice(t *testing.T) {
	vm, _ := newRunnerViewModel(t, func(c core.Command) (core.Result, error) { return core.Result{}, nil })
	vm.DetailTab = detailFiles
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	if !tt.HasText("连接后才能浏览设备文件。") {
		t.Fatalf("an unconnected device shows no explanation; texts = %v", tt.Texts())
	}
	if tt.HasText("权限") {
		t.Fatal("an unconnected device must not draw the file table")
	}
}

func TestFilePageNamesAnEmptyFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, func(string) (core.Result, error) { return core.Result{}, nil })
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "这个文件夹是空的")
}

func TestFilePageWarnsWhenTheListingIsTruncated(t *testing.T) {
	vm, _ := newFilesViewModel(t, func(string) (core.Result, error) {
		return core.Result{Output: sdcardListing + "@@TRUNCATED\n"}, nil
	})
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "只显示前 2000 项")
}

func TestFolderRowOpensTheFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	// The tree starts collapsed, so this row is the table's.
	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Click(Download) error: %v", err)
	}
	waitForText(t, tt, "update.zip")
	if !tt.HasText("50.0 MB") {
		t.Fatalf("the downloaded folder size is missing; texts = %v", tt.Texts())
	}
}

func TestFolderTreeExpandsOnlyWhenClicked(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	// /sdcard starts open, so its folders are already rows; /system's are not
	// until the user opens it.
	for _, row := range treeRows(vm) {
		if row == "/system/bin" {
			t.Fatalf("/system was open without being asked; rows = %v", treeRows(vm))
		}
	}
	if err := tt.Click("system"); err != nil {
		t.Fatalf("Click(system) error: %v", err)
	}
	waitForRow(t, vm, tt, "/system/bin")

	// /system/bin holds no folders, so opening it adds nothing.
	if err := tt.Click("bin"); err != nil {
		t.Fatalf("Click(bin) error: %v", err)
	}
	tt.Frame()
	for _, row := range treeRows(vm) {
		if strings.HasPrefix(row, "/system/bin/") {
			t.Fatalf("the empty folder expanded into %q; rows = %v", row, treeRows(vm))
		}
	}
}

func TestFileTreeReadsEachFolderOnce(t *testing.T) {
	vm, commands := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	for i := 0; i < 3; i++ {
		if err := tt.Click("system"); err != nil {
			t.Fatalf("Click(system) error: %v", err)
		}
		tt.Frame()
	}

	reads := 0
	for _, command := range commands() {
		if strings.Contains(command, "cd '/system'") {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("/system was read %d times, want 1; commands = %v", reads, commands())
	}
}

func TestFilePageShowsAReadFailureInline(t *testing.T) {
	vm, _ := newFilesViewModel(t, func(string) (core.Result, error) {
		return core.Result{ExitCode: 9}, nil
	})
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "读取失败：目录不存在或不可读：/sdcard")

	if !tt.HasText("重试") {
		t.Fatalf("a failed read offers no retry; texts = %v", tt.Texts())
	}
}

func TestFileRowOpensTheAttributeDialog(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	if err := tt.Click("照片.jpg"); err != nil {
		t.Fatalf("Click(照片.jpg) error: %v", err)
	}
	tt.Frame()

	for _, want := range []string{"文件属性", "文件名", "文件大小", "2.3 MB", "最后修改时间", "文件权限", "-rw-rw---- (660)",
		"下载到本地…", "上传到此文件夹…"} {
		if !tt.HasText(want) {
			t.Fatalf("the attribute dialog has no %q; texts = %v", want, tt.Texts())
		}
	}
	if tt.HasText("创建时间") {
		t.Fatalf("the dialog must not claim a creation time; texts = %v", tt.Texts())
	}
}

func TestAttributeDialogPullsAFileToTheChosenPath(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	original := saveLocalPath
	t.Cleanup(func() { saveLocalPath = original })
	saveLocalPath = func(mygo.SaveDialogOptions) (string, error) { return "/tmp/x.jpg", nil }

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	if err := tt.Click("照片.jpg"); err != nil {
		t.Fatalf("Click(照片.jpg) error: %v", err)
	}
	tt.Frame()
	if err := tt.Click("下载到本地…"); err != nil {
		t.Fatalf("Click(下载到本地…) error: %v", err)
	}

	if !hasTask(vm, "下载 照片.jpg") {
		t.Fatalf("no download task; tasks = %+v", vm.Manager.Tasks())
	}
}

func TestAttributeDialogDoesNotPullWhenThePickerIsCancelled(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	original := saveLocalPath
	t.Cleanup(func() { saveLocalPath = original })
	saveLocalPath = func(mygo.SaveDialogOptions) (string, error) { return "", nil }

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	if err := tt.Click("照片.jpg"); err != nil {
		t.Fatalf("Click(照片.jpg) error: %v", err)
	}
	tt.Frame()
	if err := tt.Click("下载到本地…"); err != nil {
		t.Fatalf("Click(下载到本地…) error: %v", err)
	}

	if hasTask(vm, "下载 照片.jpg") {
		t.Fatal("a cancelled save dialog must not start a download")
	}
}

func TestUploadStartsATaskForEveryChosenFile(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	original := openLocalPaths
	t.Cleanup(func() { openLocalPaths = original })
	openLocalPaths = func(mygo.OpenDialogOptions) ([]string, error) {
		return []string{"/tmp/a.txt", "/tmp/b.txt"}, nil
	}

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	if err := tt.Click("上传到此文件夹…"); err != nil {
		t.Fatalf("Click(上传到此文件夹…) error: %v", err)
	}

	for _, want := range []string{"上传 a.txt", "上传 b.txt"} {
		if !hasTask(vm, want) {
			t.Fatalf("no %q task; tasks = %+v", want, vm.Manager.Tasks())
		}
	}
}

// hasTask reports whether the manager lists a task with this title.
func hasTask(vm *ViewModel, title string) bool {
	for _, task := range vm.Manager.Tasks() {
		if task.Title == title {
			return true
		}
	}
	return false
}

// waitForCommand frames until the recorded commands contain want.
func waitForCommand(t *testing.T, commands func() []string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, command := range commands() {
			if strings.Contains(command, want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("never ran %q; commands = %v", want, commands())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestFilePagePrefetchesTheNextLevel is the whole point of reading ahead: the
// folder the user is about to click is already loaded when they click it.
func TestFilePagePrefetchesTheNextLevel(t *testing.T) {
	vm, commands := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	waitForCommand(t, commands, "cd '/sdcard/Download'")
	waitForCommand(t, commands, "cd '/sdcard/DCIM'")
}

// TestFilePageWaitsForTheUserAfterAFailedRead pins the hang that made an
// unreadable folder (/cache) sit on 「正在读取…」 forever: the page asked for a
// retry on every frame, and each retry cleared the reason it had just recorded.
func TestFilePageWaitsForTheUserAfterAFailedRead(t *testing.T) {
	vm, commands := newFilesViewModel(t, func(string) (core.Result, error) {
		return core.Result{ExitCode: 9, Output: "/system/bin/sh: cd: /sdcard: Permission denied\n"}, nil
	})
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "读取失败：没有权限查看该文件夹：/sdcard")

	for i := 0; i < 20; i++ {
		tt.Frame()
	}
	reads := 0
	for _, command := range commands() {
		if strings.Contains(command, "cd '/sdcard'") {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("/sdcard was read %d times without the user asking again, want 1", reads)
	}
}

// TestFolderTreeReadsWhenTheArrowExpandsIt covers the other way a node opens:
// the disclosure arrow, which does not activate the row.
func TestFolderTreeReadsWhenTheArrowExpandsIt(t *testing.T) {
	vm, commands := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	state := vm.fileStateFor(vm.Selection)
	state.tree.Open.Add("/system")
	tt.Frame()

	waitForCommand(t, commands, "cd '/system'")
}

// TestFileBreadcrumbsGoBack checks that the path above the table is a way back
// up the tree, not just a label.
func TestFileBreadcrumbsGoBack(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Click(Download) error: %v", err)
	}
	waitForText(t, tt, "update.zip")
	if !tt.HasText("sdcard") {
		t.Fatalf("the breadcrumb lost its ancestor; texts = %v", tt.Texts())
	}

	if err := tt.Click("sdcard"); err != nil {
		t.Fatalf("Click(sdcard) error: %v", err)
	}
	waitForText(t, tt, "照片.jpg")
}

// treeRows reports the paths the tree shows, top to bottom.
func treeRows(vm *ViewModel) []string {
	state := vm.fileStateFor(vm.Selection)
	rows := make([]string, 0, state.tree.Rows())
	for i := 0; i < state.tree.Rows(); i++ {
		rows = append(rows, state.tree.Item(i))
	}
	return rows
}

// waitForRow frames until the tree shows this path as a row.
func waitForRow(t *testing.T, vm *ViewModel, tt *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tt.Frame()
		for _, row := range treeRows(vm) {
			if row == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tree never showed %q; rows = %v", want, treeRows(vm))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestTreeChildrenKeepsTheArrowForFoldersThatCanStillExpand is the rule behind
// the disclosure arrow: unknown keeps it, known-empty drops it, and an empty
// folder that is already open keeps it so it can be closed again.
func TestTreeChildrenKeepsTheArrowForFoldersThatCanStillExpand(t *testing.T) {
	if kids := treeChildren(nil, false); kids == nil {
		t.Fatal("a folder nobody has read yet must keep its arrow")
	}
	if kids := treeChildren([]string{"/sdcard/DCIM"}, true); len(kids) != 1 {
		t.Fatalf("kids = %v", kids)
	}
	if kids := treeChildren(nil, true); kids != nil {
		t.Fatalf("a folder known to hold nothing must show no arrow, got %v", kids)
	}
}

// TestFileTreeStaysConsistentWhenTwoFoldersShareAName is the console error the
// user saw: every tree row is a child of one column, and a tree keyed by label
// gives /sdcard/Download and /system/Download one shared state.
func TestFileTreeStaysConsistentWhenTwoFoldersShareAName(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	read := func(dir string) (core.Result, error) {
		shared := `directory|4096|770|drwxrwx---|1700000000|./Download
regular file|12|660|-rw-rw----|1700000000|./note.txt
`
		switch dir {
		case "/":
			return core.Result{Output: `symbolic link|4|777|lrwxrwxrwx|1700000000|./sdcard
directory|4096|755|drwxr-xr-x|1700000000|./system
`}, nil
		case "/sdcard", "/system":
			return core.Result{Output: shared}, nil
		}
		return core.Result{}, nil
	}
	vm, _ := newFilesViewModel(t, read)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "note.txt")

	if err := tt.Click("system"); err != nil {
		t.Fatalf("Click(system) error: %v", err)
	}
	waitForRow(t, vm, tt, "/system/Download")
	for i := 0; i < 3; i++ {
		tt.Frame()
	}

	if strings.Contains(logged.String(), "have the key") {
		t.Fatalf("two rows share a key: %s", logged.String())
	}
	seen := map[string]bool{}
	for _, row := range treeRows(vm) {
		if seen[row] {
			t.Fatalf("the tree shows %q twice; rows = %v", row, treeRows(vm))
		}
		seen[row] = true
	}
}

// TestFileTreeOpensTheFolderTheTableNavigatedTo keeps the two panes agreeing: a
// folder the table opened must be visible in the tree, or the user cannot find
// their way back to it.
func TestFileTreeOpensTheFolderTheTableNavigatedTo(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Click(Download) error: %v", err)
	}
	waitForText(t, tt, "update.zip")
	// A folder the table opened must be a row the tree shows, or the user
	// cannot find their way back to it.
	waitForRow(t, vm, tt, "/sdcard/Download")
	if !vm.fileStateFor(vm.Selection).tree.Open.Has("/sdcard/Download") {
		t.Fatalf("the tree did not open the folder the table navigated to; rows = %v", treeRows(vm))
	}
}

// TestFoldingABranchForgetsWhatItHid pins the user's report: 展开 / 就看到
// /sdcard 被选中并展开. The fold closed the branch but kept the expansion flags
// underneath it, so opening / again resurrected /sdcard and its folders — a
// subtree nobody had opened in this frame.
func TestFoldingABranchForgetsWhatItHid(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	state := vm.fileStateFor(vm.Selection)

	if !state.tree.Open.Has("/sdcard") {
		t.Fatalf("the page opens with /sdcard expanded; rows = %v", treeRows(vm))
	}
	state.tree.Open.Remove("/")
	tt.Frame()
	state.tree.Open.Add("/")
	tt.Frame()

	want := []string{"/", "/sdcard", "/storage", "/system"}
	if got := treeRows(vm); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v: a reopened branch shows its children closed", got, want)
	}
}

// TestFoldingKeepsASiblingOpen checks that forgetting what a fold hid stops at
// the folded branch: the folders beside it keep the state the user gave them.
func TestFoldingKeepsASiblingOpen(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	state := vm.fileStateFor(vm.Selection)

	state.tree.Open.Add("/system")
	waitForRow(t, vm, tt, "/system/bin")
	state.tree.Open.Remove("/sdcard")
	tt.Frame()

	if !state.tree.Open.Has("/system") {
		t.Fatalf("folding /sdcard closed /system too; rows = %v", treeRows(vm))
	}
}

// TestFileTreeReopensTheRootTheTableNavigatedFrom pins the user's report: after
// collapsing / by hand, opening a folder in the table left the tree sitting on
// one collapsed row while the breadcrumbs moved deeper, so the two panes
// stopped agreeing. Navigating from the table is the user asking to see that
// folder, so the root opens again.
func TestFileTreeReopensTheRootTheTableNavigatedFrom(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	state := vm.fileStateFor(vm.Selection)
	state.tree.Open.Remove("/")
	tt.Frame()
	if rows := treeRows(vm); len(rows) != 1 || rows[0] != "/" {
		t.Fatalf("a collapsed root should be the tree's only row; rows = %v", rows)
	}

	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Click(Download) error: %v", err)
	}
	waitForText(t, tt, "update.zip")
	waitForRow(t, vm, tt, "/sdcard/Download")
}

// TestFileTreeHighlightsTheFolderTheTableOpened keeps one highlight in the
// page: the tree's selected row moves with the breadcrumbs instead of staying
// on whichever row happened to be chosen first, usually /.
//
// The click itself is not used here because both panes label a folder with its
// base name, so a label click can land on either; activateEntry is the call the
// table row makes, and the tree half is what this pins.
func TestFileTreeHighlightsTheFolderTheTableOpened(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	profile := vm.Manager.Profiles()[0]
	state := vm.fileStateFor(profile.ID)
	vm.activateEntry(profile, state, core.RemoteEntry{Name: "Download", Path: "/sdcard/Download", Dir: true})
	waitForText(t, tt, "update.zip")
	waitForRow(t, vm, tt, "/sdcard/Download")

	if state.tree.List.Selected == nil {
		t.Fatal("the tree never built a selection")
	}
	row := *state.tree.List.Selected
	if row < 0 || row >= state.tree.Rows() {
		t.Fatalf("the tree highlights row %d of %d; rows = %v", row, state.tree.Rows(), treeRows(vm))
	}
	if got := state.tree.Item(row); got != "/sdcard/Download" {
		t.Fatalf("the tree highlights %q while the table shows %q", got, state.path)
	}
}

// TestFileTreeScrollsTheFolderIntoView covers the case the highlight test
// cannot: a folder below the fold. Selecting a row nobody can see is the same
// as not selecting it.
func TestFileTreeScrollsTheFolderIntoView(t *testing.T) {
	// / holds enough folders to push /sdcard past the bottom of the pane.
	read := func(dir string) (core.Result, error) {
		if dir == "/" {
			var out strings.Builder
			for i := 0; i < 40; i++ {
				fmt.Fprintf(&out, "directory|4096|755|drwxr-xr-x|1700000000|./a%02d\n", i)
			}
			out.WriteString("symbolic link|4|777|lrwxrwxrwx|1700000000|./sdcard\n")
			return core.Result{Output: out.String()}, nil
		}
		return readFixture(dir)
	}
	vm, _ := newFilesViewModel(t, read)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	waitForRow(t, vm, tt, "/sdcard")

	state := vm.fileStateFor(vm.Selection)
	row := -1
	for i := 0; i < state.tree.Rows(); i++ {
		if state.tree.Item(i) == "/sdcard" {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("the tree never showed /sdcard; rows = %v", treeRows(vm))
	}
	// Visible reports both ends inclusively.
	first, last := state.tree.List.Visible()
	if row < first || row > last {
		t.Fatalf("row %d (/sdcard) is outside the visible range %d..%d; the tree did not scroll to the folder it highlights", row, first, last)
	}
}

// TestActivatingAFolderRowOpensIt pins the branch the two panes share: a folder
// navigates, a file opens the attribute dialog.
func TestActivatingAFolderRowOpensIt(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	state := vm.fileStateFor(profile.ID)

	vm.activateEntry(profile, state, core.RemoteEntry{Name: "Download", Path: "/sdcard/Download", Dir: true})
	if state.path != "/sdcard/Download" || vm.FileDetail != nil {
		t.Fatalf("path = %q, dialog = %v", state.path, vm.FileDetail)
	}

	vm.activateEntry(profile, state, core.RemoteEntry{Name: "log.txt", Path: "/sdcard/log.txt"})
	if vm.FileDetail == nil || vm.FileDetail.Entry.Name != "log.txt" {
		t.Fatalf("a file row must open its attributes, dialog = %v", vm.FileDetail)
	}
}

// TestFileTreeMirrorsTheDeviceFilesystemFromOneRoot pins the shape the user
// asked for: /sdcard, /storage and /system live *inside* /, not beside it as
// pinned shortcuts that showed the same directory twice.
func TestFileTreeMirrorsTheDeviceFilesystemFromOneRoot(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")
	waitForRow(t, vm, tt, "/sdcard/DCIM")

	rows := treeRows(vm)
	if len(rows) == 0 || rows[0] != "/" {
		t.Fatalf("the tree must start at /; rows = %v", rows)
	}
	// /sdcard is a symbolic link on a real device; the tree must follow it,
	// and it must sort beside the directories rather than after the files.
	want := []string{"/", "/sdcard", "/sdcard/DCIM", "/sdcard/Download", "/storage", "/system"}
	if strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row] {
			t.Fatalf("the tree shows %q twice; rows = %v", row, rows)
		}
		seen[row] = true
	}
}

// TestFileTreePrefetchesInParallel pins the shape of the read-ahead: a folder
// with many subfolders is read eight at a time, not one shell round trip after
// another. Sequentially, a big /sdcard grew its arrows over tens of seconds.
func TestFileTreePrefetchesInParallel(t *testing.T) {
	const folders = 20
	var mu sync.Mutex
	inFlight, peak, started := 0, 0, 0
	read := func(dir string) (core.Result, error) {
		mu.Lock()
		inFlight++
		started++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		// A real device takes milliseconds per folder; without a delay here the
		// reads would never overlap and the test would mean nothing.
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		if dir == "/" {
			var out strings.Builder
			for i := 0; i < folders; i++ {
				fmt.Fprintf(&out, "directory|4096|755|drwxr-xr-x|1700000000|./a%02d\n", i)
			}
			return core.Result{Output: out.String()}, nil
		}
		return core.Result{}, nil
	}

	vm, _ := newFilesViewModel(t, read)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		seen := started
		mu.Unlock()
		if seen >= 1+folders {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %d of %d folders", seen, 1+folders)
		}
		tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}

	mu.Lock()
	got := peak
	mu.Unlock()
	// One at a time can only ever overlap the two folders the page opens on its
	// own (/ and /sdcard), so a peak near the cap is the whole point.
	if got < prefetchParallel/2 {
		t.Fatalf("read-ahead ran folders one at a time (peak %d in flight)", got)
	}
	if got > prefetchParallel {
		t.Fatalf("peak %d folder reads in flight, want at most %d", got, prefetchParallel)
	}
}

// 重连来临时正在进行的目录读取会撞上「还没进 device 状态」。那是过渡态：
// 页面不许留下「读取失败」，连接恢复后自己重读。
func TestFileReadDuringAReconnectIsNotAFailure(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	vm.Manager.ForceSessionStateForTest(profile.ID, core.StateConnecting, "")
	vm.loadDir(profile, "/sdcard", true)
	waitUntil(t, "the blocked read to finish", func() bool {
		return !vm.fileLoading(fileKey(profile.ID, "/sdcard"))
	})
	if reason := vm.fileFailure(fileKey(profile.ID, "/sdcard")); reason != "" {
		t.Fatalf("a transition state was cached as a failure: %q", reason)
	}

	vm.Manager.ForceSessionStateForTest(profile.ID, core.StateOnline, "")
	tt.Frame()
	if joined := strings.Join(tt.Texts(), "\n"); strings.Contains(joined, "读取失败") {
		t.Fatalf("the page kept a failure from a reconnect; texts = %v", tt.Texts())
	}
}

// 一次新连接从设备根开始：没有文件夹被自动打开（这就是"另一台设备打开的
// sdcard 也在这里打开了"的来源）。
func TestNewFilePageStartsAtTheDeviceRoot(t *testing.T) {
	vm, _ := newConnectedFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	if got := vm.fileStateFor(profile.ID).path; got != "/" {
		t.Fatalf("the page starts on %q, want the device root", got)
	}
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "system")
	if tt.HasText("照片.jpg") {
		t.Fatalf("a fresh connection opened /sdcard by itself; texts = %v", tt.Texts())
	}
}

// 同一次连接里目录与展开状态要记住；换了连接就回到根。
func TestFilePageReturnsToTheRootForANewConnection(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard/Download")

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "update.zip") // 同一连接内确实记着

	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	waitForText(t, tt, "system") // 新连接：回到 /

	if got := vm.fileStateFor(profile.ID).path; got != "/" {
		t.Fatalf("the new connection reused the old folder: %q", got)
	}
	if tt.HasText("update.zip") {
		t.Fatalf("the old folder's rows are still on screen; texts = %v", tt.Texts())
	}
}

// 同一个设备可以有两个独立浏览器：文件页与「选择设备路径」。
func TestPickerKeepsItsOwnFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	picker := vm.pickerStateFor(profile.ID)
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard/Download")
	vm.showDir(profile, picker, "/system")

	if got := vm.fileStateFor(profile.ID).path; got != "/sdcard/Download" {
		t.Fatalf("the file page moved with the picker: %q", got)
	}
	if got := picker.path; got != "/system" {
		t.Fatalf("the picker lost its own folder: %q", got)
	}
}

// 文件页与选择器共用一套渲染：选择器里的表格也用同一套列。
func TestPickerRendersTheSameColumnsAsTheFilePage(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	picker := vm.pickerStateFor(profile.ID)
	vm.showDir(profile, picker, "/sdcard")
	tt := ui.NewTester(func(c *ui.Context) {
		vm.pickerPane(c, profile, picker, pickFolder, nil)
	}, 760, 420)
	waitForText(t, tt, "照片.jpg")
	for _, want := range []string{"名称", "大小", "修改时间", "权限", "sdcard"} {
		if !tt.HasText(want) {
			t.Fatalf("the picker has no %q; texts = %v", want, tt.Texts())
		}
	}
}

// pickerFixture is a picker on / whose tree shows one folded root, so a click
// can only land on the table's rows.
func pickerFixture(t *testing.T) (*ui.Tester, *fileState) {
	t.Helper()
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	picker := vm.pickerStateFor(profile.ID)
	vm.showDir(profile, picker, "/")
	picker.tree.Open.Remove("/")
	tt := ui.NewTester(func(c *ui.Context) {
		vm.pickerPane(c, profile, picker, pickFolder, nil)
	}, 760, 420)
	waitForText(t, tt, "system")
	return tt, picker
}

// 单击行内任意位置都要进得去，包括文字下沿那片留白：用户点的是「这一行」，
// 不是「这一行的字」。（曾经点下沿只会选中，看起来像点不动。）
func TestClickOnTheBottomEdgeOfARowOpensTheFolder(t *testing.T) {
	// 文字高 16 DIP、行高 32：文字下沿再往下 2–6 DIP 仍在行内。
	for _, dy := range []float32{2, 4, 6} {
		tt, picker := pickerFixture(t)
		rect, ok := tt.Find("system")
		if !ok {
			t.Fatal("the picker table has no system row")
		}
		tt.ClickAt(rect.X+40, rect.Y+rect.H+dy)
		tt.Frame()
		tt.Frame()
		if picker.path != "/system" {
			t.Fatalf("a click %.0f DIP below the label did not open the folder: path=%q", dy, picker.path)
		}
	}
}
