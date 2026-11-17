package app

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// Rendering the window to PNGs.
//
// This is not an assertion harness: nothing here fails because a pixel moved,
// and it writes nothing unless TAILCAT_SHOT_DIR is set. It exists because some
// questions about this app can only be answered by looking — is the file pane
// actually framed, does the tree indent its notes, is a control reachable, did
// the dialog lay out on top of the page. A view test can tell you the text is
// there; only the picture tells you it is readable.
//
//	TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots -v
//
// Add a case whenever a page or a dialog gains a state worth seeing.

// shotCase is one window state worth capturing.
type shotCase struct {
	name string
	// build returns the view model positioned on the state to capture.
	build func(t *testing.T) *ViewModel
	// view defaults to ContentView, which draws the page, its dialogs and its
	// toasts. Set it to capture a shell slot on its own (the sidebar).
	view func(*ViewModel) func(*ui.Context)
	// width and height default to 1000x760.
	width, height int
	// settle is how many frames to draw before capturing. Asynchronous work
	// (a directory read, a reconnect) needs a few; dialogs animate in.
	settle int
}

func (s shotCase) frames() int {
	if s.settle > 0 {
		return s.settle
	}
	return 3
}

func (s shotCase) size() (int, int) {
	width, height := s.width, s.height
	if width == 0 {
		width = 1000
	}
	if height == 0 {
		height = 760
	}
	return width, height
}

// TestScreenshots renders every case to a PNG under TAILCAT_SHOT_DIR.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("TAILCAT_SHOT_DIR")
	if dir == "" {
		t.Skip("set TAILCAT_SHOT_DIR to a directory to render the window as PNGs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	cases := shotCases()
	for _, shot := range cases {
		t.Run(shot.name, func(t *testing.T) {
			vm := shot.build(t)
			view := vm.ContentView
			if shot.view != nil {
				view = shot.view(vm)
			}
			width, height := shot.size()
			tt := ui.NewTester(view, width, height)
			for i := 0; i < shot.frames(); i++ {
				// The tester's clock is virtual, so a frame does not let a
				// goroutine land; the sleep is what makes the read arrive.
				tt.Advance(20 * time.Millisecond)
				tt.Frame()
				time.Sleep(2 * time.Millisecond)
			}
			path := filepath.Join(dir, shot.name+".png")
			writeShot(t, path, tt.Image())
			t.Log(path)
		})
	}
}

// writeShot saves one frame.
func writeShot(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// shotCases is every state worth looking at, in reading order.
func shotCases() []shotCase {
	return []shotCase{
		{
			name:  "01-device-index",
			build: func(t *testing.T) *ViewModel { vm := shotConnected(t, readFixture); vm.OpenDevices(); return vm },
		},
		{
			name:  "02-sidebar",
			build: func(t *testing.T) *ViewModel { return shotConnected(t, readFixture) },
			view: func(vm *ViewModel) func(*ui.Context) {
				return func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() {
						vm.SidebarView(c)
						vm.SidebarFooter(c)
					})
				}
			},
			width: 300, height: 760,
		},
		{
			name:  "03-device-overview",
			build: func(t *testing.T) *ViewModel { return shotDevice(t, readFixture, detailOverview) },
		},
		{
			name:   "04-device-resources",
			build:  func(t *testing.T) *ViewModel { vm, _ := resourceFixture(t); return vm },
			settle: 6,
		},
		{
			name:  "05-device-files",
			build: func(t *testing.T) *ViewModel { return shotDevice(t, readFixture, detailFiles) },
			// Directory reads run off the render path; give them a moment.
			settle: 20,
		},
		{
			name: "06-device-files-failed",
			build: func(t *testing.T) *ViewModel {
				return shotDevice(t, func(dir string) (core.Result, error) {
					if dir == "/sdcard" {
						return core.Result{ExitCode: 9, Output: "cd: /sdcard: Permission denied\n"}, nil
					}
					return readFixture(dir)
				}, detailFiles)
			},
			settle: 20,
		},
		{
			name: "07-device-files-loading",
			build: func(t *testing.T) *ViewModel {
				release := make(chan struct{})
				t.Cleanup(func() { close(release) })
				return shotDevice(t, func(dir string) (core.Result, error) {
					if dir == "/sdcard" {
						<-release
					}
					return readFixture(dir)
				}, detailFiles)
			},
		},
		{
			name: "08-file-attributes",
			build: func(t *testing.T) *ViewModel {
				vm := shotDevice(t, readFixture, detailFiles)
				vm.FileDetail = &FileDetail{
					Device: vm.Selection,
					Entry: core.RemoteEntry{
						Name:     "照片.jpg",
						Path:     "/sdcard/照片.jpg",
						Size:     2411724,
						Mode:     "-rw-rw----",
						Octal:    "660",
						Modified: time.Unix(1700000000, 0),
					},
				}
				return vm
			},
			settle: 12,
		},
		{
			name:   "09-device-logs",
			build:  func(t *testing.T) *ViewModel { return shotDevice(t, readFixture, detailLog) },
			settle: 6,
		},
		{
			name: "10-device-editor",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				profile := vm.Manager.Profiles()[0]
				vm.openEditor(&EditorRequest{Profile: &profile})
				return vm
			},
			settle: 12,
		},
		{
			name: "11-temporary-editor",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				vm.openEditor(&EditorRequest{Temporary: true})
				return vm
			},
			settle: 12,
		},
		{
			name: "12-tool-settings",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				vm.openSettings()
				return vm
			},
			settle: 12,
		},
		{
			name: "13-reachability",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				vm.openReachability()
				return vm
			},
			settle: 12,
		},
		{
			name: "14-attention",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				profile := vm.Manager.Profiles()[0]
				vm.Manager.ForceAttentionForTest(profile.ID, core.AttentionSerialDetached,
					"本应用建立的 ADB 连接已被外部断开（adb disconnect）")
				return vm
			},
			settle: 12,
		},
	}
}

// shotConnected is a view model with one connected device.
func shotConnected(t *testing.T, read func(dir string) (core.Result, error)) *ViewModel {
	t.Helper()
	vm, _ := newFilesViewModel(t, read)
	return vm
}

// shotDevice is shotConnected positioned on one tab of the device page.
func shotDevice(t *testing.T, read func(dir string) (core.Result, error), tab int) *ViewModel {
	t.Helper()
	vm := shotConnected(t, read)
	vm.DetailTab = tab
	vm.OpenDevice(vm.Selection)
	return vm
}
