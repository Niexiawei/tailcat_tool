package app

import (
	"context"
	"strings"
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

// readFixture answers every directory the default fixtures describe.
func readFixture(dir string) (core.Result, error) {
	switch dir {
	case "/sdcard":
		return core.Result{Output: sdcardListing}, nil
	case "/system":
		return core.Result{Output: systemListing}, nil
	case "/sdcard/Download":
		return core.Result{Output: downloadListing}, nil
	}
	return core.Result{}, nil
}

// newFilesViewModel builds a connected device whose directory reads are answered
// by read. It returns the recorded commands so a test can count them.
func newFilesViewModel(t *testing.T, read func(dir string) (core.Result, error)) (*ViewModel, func() []string) {
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

	// The roots are named by their whole path, which nothing else on the
	// page is, so this click can only land on the tree.
	if err := tt.Click("/system"); err != nil {
		t.Fatalf("Click(/system) error: %v", err)
	}
	waitForText(t, tt, "build.prop")

	if err := tt.Click("bin"); err != nil {
		t.Fatalf("Click(bin) error: %v", err)
	}
	// /system/bin lists nothing, so the expanded node says so. That sentence
	// only exists inside the tree.
	waitForText(t, tt, "没有子文件夹")
}

func TestFileTreeReadsEachFolderOnce(t *testing.T) {
	vm, commands := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	for i := 0; i < 3; i++ {
		if err := tt.Click("/system"); err != nil {
			t.Fatalf("Click(/system) error: %v", err)
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
	*state.openAt("/system") = true
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

// TestFileTreeNotesLineUpWithTheirFolder keeps the loading and empty lines from
// reading as the first row of the whole tree.
func TestFileTreeNotesLineUpWithTheirFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	if err := tt.Click("/system"); err != nil {
		t.Fatalf("Click(/system) error: %v", err)
	}
	waitForText(t, tt, "build.prop")
	if err := tt.Click("bin"); err != nil {
		t.Fatalf("Click(bin) error: %v", err)
	}
	waitForText(t, tt, "没有子文件夹")

	note, ok := tt.Find("没有子文件夹")
	if !ok {
		t.Fatalf("no note to measure; texts = %v", tt.Texts())
	}
	root, ok := tt.Find("/system")
	if !ok {
		t.Fatalf("no root to compare with; texts = %v", tt.Texts())
	}
	if note.X <= root.X+10 {
		t.Fatalf("the note is flush with the tree's edge: note.X = %v, root.X = %v", note.X, root.X)
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
	// /sdcard/Download holds no folders, so this sentence can only come from
	// the tree — and only if the node is open.
	waitForText(t, tt, "没有子文件夹")
}
