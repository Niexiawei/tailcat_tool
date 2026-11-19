package app

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
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
			// Both panes must agree: the breadcrumbs, the table and the tree
			// highlight all on the folder the user entered from the table.
			name: "05b-device-files-navigated",
			build: func(t *testing.T) *ViewModel {
				vm := shotDevice(t, readFixture, detailFiles)
				profile := vm.Manager.Profiles()[0]
				vm.activateEntry(profile, vm.fileStateFor(profile.ID), core.RemoteEntry{
					Name: "Download", Path: "/sdcard/Download", Dir: true,
				})
				return vm
			},
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
			// The device tab and the page share one view; this is the tab's
			// own frame: no device picker, following by default.
			name:   "09-device-logs",
			build:  shotDeviceLogTab,
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
				vm.OpenTools()
				return vm
			},
			settle: 6,
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
			name: "13b-reachability-results",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				vm.openReachability()
				vm.Manager.TestAllDevices()
				vm.Manager.Reachability().Wait()
				return vm
			},
			settle: 12,
		},
		{
			name:  "15-logs-page",
			build: shotLogsPage,
		},
		{
			name: "16-logs-page-empty",
			build: func(t *testing.T) *ViewModel {
				vm := shotLogsPage(t)
				// The field holds the draft and Search is what it applied, so
				// a state set up without the UI sets both.
				filter := vm.pageLogFilter()
				filter.searchDraft, filter.Search = "这段时间没有的关键词", "这段时间没有的关键词"
				return vm
			},
		},
		{
			name:   "17-task-output",
			build:  shotTaskOutput,
			settle: 6,
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
		{
			// "adb kill-server" with three devices online: one prompt, one
			// action for the whole fleet.
			name: "14b-attention-fleet",
			build: func(t *testing.T) *ViewModel {
				vm := shotConnected(t, readFixture)
				ids := []uuid.UUID{vm.Manager.Profiles()[0].ID}
				for _, name := range []string{"EX5", "客厅"} {
					id, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
						Name: name, Address: testAddress, RemotePort: 5555,
					})
					if err != nil {
						t.Fatalf("SaveDevice(%q) error: %v", name, err)
					}
					ids = append(ids, id)
				}
				vm.Manager.ForceAttentionGroupForTest(ids, core.AttentionServerRestart, "3 台设备的本地 ADB 入口同时消失")
				return vm
			},
			settle: 12,
		},
		{
			name: "18-derp-list",
			build: func(t *testing.T) *ViewModel {
				vm, _ := newDerpViewModel(t)
				if _, err := vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{
					Name: "备用地图", Source: "https://relay2.example.com/map.json",
				}); err != nil {
					t.Fatalf("SaveRelayPreset() error: %v", err)
				}
				return vm
			},
		},
		{
			name: "18b-derp-list-long",
			build: func(t *testing.T) *ViewModel {
				// Enough presets that the box around the list has to scroll: the
				// page's whole point is that the top half does not move with it.
				vm, _ := newDerpViewModel(t)
				saveDerpPresets(t, vm, 12)
				return vm
			},
		},
		{
			name: "19-derp-detail",
			build: func(t *testing.T) *ViewModel {
				vm := newTestViewModel(t)
				id, err := vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{Name: "公司自建", Source: derpTestMap})
				if err != nil {
					t.Fatalf("SaveRelayPreset() error: %v", err)
				}
				vm.OpenDerpDetail(id)
				vm.DerpJSONOpen = true
				return vm
			},
			settle: 6,
		},
		{
			name: "20-derp-editor",
			build: func(t *testing.T) *ViewModel {
				vm := newTestViewModel(t)
				vm.OpenDerp()
				vm.openDerpEditor(&DerpEditorDraft{Name: "公司自建", Kind: "json", Content: derpTestMap})
				return vm
			},
			settle: 12,
		},
		{
			name: "24-device-editor-relay",
			build: func(t *testing.T) *ViewModel {
				vm, preset := newDerpViewModel(t)
				vm.openEditor(&EditorRequest{})
				vm.EditorDraft.RelayTab = 1
				vm.EditorDraft.RelayPick = preset.Source
				vm.EditorDraft.RelaySource = preset.Source
				return vm
			},
			settle: 12,
		},
		{
			name: "25-device-editor-relay-custom",
			build: func(t *testing.T) *ViewModel {
				vm, _ := newDerpViewModel(t)
				vm.openEditor(&EditorRequest{})
				vm.EditorDraft.RelayTab = 1
				vm.EditorDraft.RelaySource = `{"Regions":{"901":{"RegionID":901,"RegionCode":"test","Nodes":[{"Name":"901a","RegionID":901,"HostName":"relay.example.com","DERPPort":443}]}}}`
				return vm
			},
			settle: 12,
		},
		{
			name: "22-appmeta-cache",
			build: func(t *testing.T) *ViewModel {
				cache := &stubAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
				vm := newCacheViewModel(t, cache)
				vm.CacheDevice = vm.Manager.Profiles()[0].ID
				cache.entries[vm.CacheDevice.String()] = []core.AppMetaEntry{
					{Package: "com.tencent.mm", Label: "微信", Size: 1258291, FetchedAt: time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local)},
					{Package: "com.android.settings", Label: "设置", Size: 512, FetchedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local), Stale: true},
				}
				return vm
			},
		},
		{
			name: "27-resources-app-actions",
			build: func(t *testing.T) *ViewModel {
				vm, _ := resourceFixtureWithMeta(t, map[string]core.AppMeta{
					"com.foo": {Package: "com.foo", Label: "微信"},
				})
				return vm
			},
			settle: 4,
		},
		{
			name: "26-appmeta-cache-long",
			build: func(t *testing.T) *ViewModel {
				cache := &stubAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
				vm := newCacheViewModel(t, cache)
				profile := vm.Manager.Profiles()[0]
				for i := 0; i < 25; i++ {
					cache.entries[profile.ID.String()] = append(cache.entries[profile.ID.String()], core.AppMetaEntry{
						Package:   fmt.Sprintf("com.example.app%02d", i),
						Label:     fmt.Sprintf("示例应用 %02d", i),
						Size:      int64(1024 * (i + 1)),
						FetchedAt: time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local),
					})
				}
				return vm
			},
			width: 900, height: 700,
		},
		{
			name: "23-appmeta-cache-confirm",
			build: func(t *testing.T) *ViewModel {
				cache := &stubAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
				vm := newCacheViewModel(t, cache)
				vm.CacheDevice = vm.Manager.Profiles()[0].ID
				cache.entries[vm.CacheDevice.String()] = []core.AppMetaEntry{
					{Package: "com.tencent.mm", Label: "微信", Size: 1258291, FetchedAt: time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local)},
				}
				vm.CacheConfirmOpen = true
				return vm
			},
			settle: 12,
		},
		{
			name:   "28-logcat-page",
			build:  shotLogcatPage,
			settle: 4,
		},
		{
			name: "29-logcat-empty",
			build: func(t *testing.T) *ViewModel {
				// No device is connected, so the page can only explain why the
				// box is empty.
				vm := newTestViewModel(t)
				vm.OpenLogcat()
				return vm
			},
		},
		{
			name: "30-logcat-paused",
			build: func(t *testing.T) *ViewModel {
				vm := shotLogcatPage(t)
				vm.Logcat.Paused = true
				return vm
			},
			settle: 4,
		},
		{
			name: "31-logcat-ended",
			build: func(t *testing.T) *ViewModel {
				vm, runner := shotLogcatStream(t)
				runner.end(1)
				return vm
			},
			settle: 6,
		},
		{
			name: "21-derp-delete",
			build: func(t *testing.T) *ViewModel {
				vm, preset := newDerpViewModel(t)
				if _, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
					Name: "客厅", Address: testAddress, RemotePort: 5555, RelaySource: preset.Source,
				}); err != nil {
					t.Fatalf("SaveDevice() error: %v", err)
				}
				wanted := preset
				vm.DerpConfirmDelete = &wanted
				return vm
			},
			settle: 12,
		},
		{
			name: "32-startup-problems",
			build: func(t *testing.T) *ViewModel {
				vm := newTestViewModel(t)
				vm.StartupOpen = true
				vm.StartupProblems = []core.Problem{
					{Kind: core.ProblemConfig, Title: "配置读取失败，已使用默认设置", Detail: "unexpected end of JSON input", Path: "C:\\Users\\me\\AppData\\Roaming\\TailcatManager", Continue: true},
					{Kind: core.ProblemHistory, Title: "历史库无法打开，本次运行不会保存历史", Detail: "disk full", Path: "C:\\Users\\me\\AppData\\Roaming\\TailcatManager", Continue: true},
					{Kind: core.ProblemADB, Title: "未找到 adb，连接设备前需要先安装", Continue: true},
				}
				vm.OpenDir = func(string) error { return nil }
				vm.Quit = func() {}
				return vm
			},
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

// shotLogcatPage is the Logcat page holding the lines a stream would have
// carried. A fake runner feeds them, so the picture does not depend on a timer.
func shotLogcatPage(t *testing.T) *ViewModel {
	vm, _ := shotLogcatStream(t)
	return vm
}

// shotLogcatStream is the page with its stream started and a batch of lines
// already in the ring.
func shotLogcatStream(t *testing.T) (*ViewModel, *logcatRunner) {
	t.Helper()
	runner := &logcatRunner{}
	vm := newBlockingViewModel(t, runner)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { vm.Manager.Disconnect(context.Background(), profile.ID) })
	vm.OpenLogcat()
	// The page starts its stream on its first frame; a screenshot has to have
	// it before the lines are fed.
	vm.ensureLogcatDevice(vm.logcatState())
	vm.ensureLogcatStream(vm.logcatState())
	runner.feed(shotLogcatLines)
	return vm, runner
}

// shotLogcatLines is what the page shows in a screenshot: every level, a few
// tags, one multi-line crash and one long message.
const shotLogcatLines = `11-18 09:30:01.123  4100  4123 I ActivityManager: Start proc 4100:com.demo.camera/u0a132 for activity
11-18 09:30:01.180  4100  4140 D OpenGLRenderer: setSurface called
11-18 09:30:01.240  4100  4140 I Choreographer: Skipped 3 frames! The application may be doing too much work on its main thread: the frame took 47 ms and the deadline was missed by 19 ms, which is a long message on purpose so the column has to wrap it instead of cutting it off
11-18 09:30:01.300  4100  4141 W demo.camera: preview callback returned 30 fps, dropping frames
11-18 09:30:01.360  4200  4200 E AndroidRuntime: FATAL EXCEPTION: main
11-18 09:30:01.360  4200  4200 E AndroidRuntime:   at com.demo.camera.Main.onResume(Main.java:42)
11-18 09:30:01.360  4200  4200 E AndroidRuntime: Caused by: java.lang.IllegalStateException: camera in use
11-18 09:30:02.000  4100  4123 I ActivityManager: Displayed com.demo.camera/.Main
11-18 09:30:02.100  1234  1250 V SurfaceFlinger: 超过缓冲上限的详细行也要能显示
11-18 09:30:02.200  4100  4123 F demo.camera: supervisor asked the app to stop
`

// shotLogsPage is the demo app on the log page: the page reads the demo's own
// in-memory history, so the picture has rows without a real device.
func shotLogsPage(t *testing.T) *ViewModel {
	t.Helper()
	return newDemoViewModel(t, func(vm *ViewModel) { vm.OpenLogs() })
}

// shotDeviceLogTab is the same history seen through one device's tab.
func shotDeviceLogTab(t *testing.T) *ViewModel {
	t.Helper()
	return newDemoViewModel(t, func(vm *ViewModel) {
		vm.DetailTab = detailLog
		vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	})
}

// shotTaskOutput opens the dialog a failed task row opens.
func shotTaskOutput(t *testing.T) *ViewModel {
	t.Helper()
	vm := newDemoViewModel(t, func(vm *ViewModel) { vm.OpenLogs() })
	if vm.History == nil {
		t.Fatal("the demo has no history")
	}
	entries, err := vm.History.Timeline(core.LogQuery{})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	for _, entry := range entries {
		if entry.Source == core.LogTask {
			vm.openLogOutput(entry)
			return vm
		}
	}
	t.Fatal("the demo history has no task row")
	return nil
}

// newDemoViewModel builds a view model over the simulated environment, so a
// screenshot never needs a real device, a real database or a network.
func newDemoViewModel(t *testing.T, position func(*ViewModel)) *ViewModel {
	t.Helper()
	env := NewDemoEnvironment()
	vm := NewViewModel(env.Manager, env.Locate)
	vm.Demo = true
	vm.History = env.History
	vm.RecordAppLog = env.RecordAppLog
	position(vm)
	return vm
}
