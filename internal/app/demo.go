package app

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

// demoRunner answers adb calls the way a healthy device would.
type demoRunner struct{}

func (demoRunner) Start(ctx context.Context, c core.Command, onOutput func(string)) (*core.Proc, error) {
	if !strings.Contains(strings.Join(c.Args, " "), "logcat") {
		return nil, core.Error("demo mode runs no processes")
	}
	// The demo prints what a device would, so the page, its filters and the
	// export have something to work on. It stops on the context because there
	// is no real process to signal: ExecRunner ignores it, the demo honours it.
	done := make(chan int, 1)
	proc := &core.Proc{Done: done}
	go func() {
		ticker := time.NewTicker(demoLogcatInterval)
		defer ticker.Stop()
		for n := 1; ; n++ {
			select {
			case <-ctx.Done():
				done <- 0
				return
			case <-ticker.C:
				onOutput(demoLogcatLine(n))
			}
		}
	}()
	return proc, nil
}

func (demoRunner) Run(_ context.Context, c core.Command, onOutput func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg == "get-state" {
			return core.Result{Output: "device\n"}, nil
		}
	}
	joined := strings.Join(c.Args, " ")
	switch {
	case strings.Contains(joined, "@@STAT"):
		return core.Result{Output: demoResourceOutput}, nil
	case strings.Contains(joined, "pm path"):
		return core.Result{Output: "package:/data/app/com.demo.camera/base.apk\n"}, nil
	case strings.Contains(joined, "cd '"):
		return core.Result{Output: demoDirListing(joined)}, nil
	case strings.Contains(joined, "stat -c"):
		return core.Result{Output: "4096 1700000000\n"}, nil
	}
	time.Sleep(50 * time.Millisecond)
	if onOutput != nil {
		onOutput("示例任务完成，未执行真实设备命令。\n")
	}
	return core.Result{Output: "示例任务完成，未执行真实设备命令。\n"}, nil
}

// demoLogcatInterval is how often the simulated device prints a line: often
// enough to read as a stream, slow enough that a screenshot is stable.
const demoLogcatInterval = 150 * time.Millisecond

// demoLogcatLine is one line the simulated device prints, in the shape
// `logcat -v threadtime` produces. The levels and tags rotate so the level
// tabs and the tag box have something to do.
func demoLogcatLine(n int) string {
	levels := []string{"I", "I", "D", "I", "W", "I", "E"}
	tags := []string{"ActivityManager", "OpenGLRenderer", "demo.camera", "Choreographer", "demo.gallery"}
	texts := []string{
		"应用已进入前台",
		"绘制第 n 帧",
		"预览回调返回 30 fps",
		"跳过了 2 帧",
		"模拟的异常：示例日志流不连接真实设备",
	}
	level := levels[(n-1)%len(levels)]
	tag := tags[(n-1)%len(tags)]
	text := texts[(n-1)%len(texts)]
	stamp := time.Now().Format("01-02 15:04:05.000")
	return stamp + "  1234  1250 " + level + " " + tag + ": 第 " + itoa(n) + " 条演示日志 · " + text + "\n"
}

// The timestamps are fixed so the page reads the same on every run.
func demoDirListing(script string) string {
	switch {
	case strings.Contains(script, "/sdcard/Download"):
		return "directory|4096|770|drwxrwx---|1700000000|./旧文件\n" +
			"regular file|52428800|660|-rw-rw----|1700000000|./update.zip\n"
	case strings.Contains(script, "/sdcard"):
		return "directory|4096|770|drwxrwx---|1700000000|./DCIM\n" +
			"directory|4096|770|drwxrwx---|1700000000|./Download\n" +
			"regular file|2048|660|-rw-rw----|1700000000|./log.txt\n" +
			"regular file|2411724|660|-rw-rw----|1700000000|./照片.jpg\n"
	case strings.Contains(script, "/data/local/tmp"):
		return "regular file|1024|600|-rw-------|1700000000|./helper.sh\n"
	}
	return ""
}

// demoOpener hands out a fake channel without touching the network.
type demoOpener struct{ next int }

func (o *demoOpener) Open(_ context.Context, req core.ForwardRequest, onLog func(string)) (*core.ForwardConn, error) {
	port := req.PreferredPort
	if port == 0 {
		port = 5556 + o.next
		o.next++
	}
	if onLog != nil {
		onLog("示例通道监听 127.0.0.1:" + itoa(port) + "\n")
	}
	return &core.ForwardConn{
		Port:   port,
		Serial: "127.0.0.1:" + itoa(port),
		Close:  func(context.Context) error { return nil },
	}, nil
}

// NewDemoEnvironment builds the simulated app used by --demo.
func NewDemoEnvironment() Environment {
	profiles := []core.Profile{
		{ID: uuid.New(), Name: "E245 · 示例", RemotePort: 5555, PreferredPort: portPtr(5556)},
		{ID: uuid.New(), Name: "EX5 · 示例", RemotePort: 5555, PreferredPort: portPtr(5557)},
	}
	creds := core.NewMemoryCredentialStore()
	for _, profile := range profiles {
		_ = creds.Write("tc"+repeatChar('A', 30), profile.ID)
	}
	// The demo never runs a process, so pointing ADB at this executable keeps
	// the tool row honest (present) without spawning anything.
	self, _ := os.Executable()
	// --demo never opens history.db, but its log page must still have rows:
	// a screenshot of an empty page proves nothing.
	demo := newDemoHistory(profiles)
	demoCache := newDemoAppMetaCache()
	manager := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: profiles,
			Tools:    core.ToolSettings{ADB: self},
		},
		Credentials:  creds,
		Identities:   core.NewIdentityStore(demoIdentityDir()),
		Runner:       demoRunner{},
		Forwarder:    &demoOpener{},
		Locate:       func(string) (string, bool) { return "", false },
		AppMeta:      demoAppMeta{},
		AppMetaCache: demoCache,
		Recorder:     demo,
	})
	return Environment{
		Manager:      manager,
		Locate:       func(string) (string, bool) { return "", false },
		Demo:         true,
		History:      demo,
		RecordAppLog: manager.RecordAppLog,
	}
}

func portPtr(v int) *int { return &v }

// demoIdentityDir keeps the simulated identities in the OS temp directory so
// demo mode never touches the real identity files.
func demoIdentityDir() string {
	dir, err := os.MkdirTemp("", "tailcat-demo-identities")
	if err != nil {
		return os.TempDir()
	}
	return dir
}

func repeatChar(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

// demoAppMetaCache gives the cache page rows to look at without touching the
// real cache directory. Purge and Invalidate really empty it, so the demo's
// clear button behaves like the real one.
type demoAppMetaCache struct {
	mu      sync.Mutex
	entries map[string][]core.AppMetaEntry
}

func newDemoAppMetaCache() *demoAppMetaCache {
	return &demoAppMetaCache{entries: map[string][]core.AppMetaEntry{}}
}

func (d *demoAppMetaCache) Entries(device string) []core.AppMetaEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.entries[device]; !ok {
		now := time.Now()
		d.entries[device] = []core.AppMetaEntry{
			{Package: "com.android.camera", Label: "相机", Size: 20 * 1024, FetchedAt: now.Add(-2 * time.Hour)},
			{Package: "com.android.gallery", Label: "相册", Size: 15 * 1024, FetchedAt: now.Add(-9 * 24 * time.Hour), Stale: true},
		}
	}
	return append([]core.AppMetaEntry(nil), d.entries[device]...)
}

func (d *demoAppMetaCache) Invalidate(device, pkg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	kept := d.entries[device][:0]
	for _, entry := range d.entries[device] {
		if entry.Package != pkg {
			kept = append(kept, entry)
		}
	}
	d.entries[device] = kept
	return nil
}

func (d *demoAppMetaCache) Purge(device string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[device] = nil
	return nil
}

// demoAppMeta gives the simulated devices names and icons without any adb: the
// real one pulls an APK, which --demo never does.
type demoAppMeta struct{}

// demoIcon is a 1×1 PNG; Avatar cover-crops it into a circle.
var demoIcon = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func (demoAppMeta) Lookup(_, pkg string) (core.AppMeta, bool) {
	switch pkg {
	case "com.demo.camera":
		return core.AppMeta{Package: pkg, Label: "相机", Icon: demoIcon}, true
	case "com.demo.gallery":
		return core.AppMeta{Package: pkg, Label: "相册", Icon: demoIcon}, true
	}
	return core.AppMeta{}, false
}

// Request and Refresh have nothing to do: the demo has no APK to read.
func (demoAppMeta) Request(string, string, string, []string) {}

func (demoAppMeta) Refresh(string, string, string, []string) {}

// Status answers for the simulated processes so the demo shows a named
// application, a system process with no APK, and an unasked row.
func (demoAppMeta) Status(_, pkg string) (core.AppMetaStatus, bool) {
	switch pkg {
	case "com.demo.camera", "com.demo.gallery":
		return core.AppMetaStatus{State: core.AppMetaReady}, true
	case "system_server":
		return core.AppMetaStatus{State: core.AppMetaFailed, Reason: core.ReasonNoAppPackage, Final: true}, true
	}
	return core.AppMetaStatus{}, false
}

func (demoAppMeta) Forget(string) {}

// demoResourceOutput is what the simulated device prints for one sampling
// round: two applications, a system process and no GPU counter.
const demoResourceOutput = `@@STAT
cpu  12000 0 3000 90000 500 0 300 0 0 0
@@MEM
MemTotal:        8000000 kB
MemAvailable:    2500000 kB
@@CPU
8
Hardware	: Qualcomm Technologies, Inc SM8150
@@GPU
@@PROC
@@P 100
100 (cameraserver) S 1 100 100 0 -1 0 0 0 0 0 900 200 0 0 20 0 12 0 5000 0 0
VmRSS:	   204800 kB
com.demo.camera 
@@P 101
101 (camera:remote) S 1 101 101 0 -1 0 0 0 0 0 120 40 0 0 20 0 4 0 5100 0 0
VmRSS:	    30720 kB
com.demo.camera:remote 
@@P 200
200 (gallery) S 1 200 200 0 -1 0 0 0 0 0 300 60 0 0 20 0 8 0 5200 0 0
VmRSS:	    81920 kB
com.demo.gallery 
@@P 300
300 (system_server) S 1 300 300 0 -1 0 0 0 0 0 800 700 0 0 20 0 40 0 4000 0 0
VmRSS:	   102400 kB
system_server 
`

// demoHistory is the simulated history: one timeline in memory, no file.
type demoHistory struct {
	mu      sync.Mutex
	entries []core.TimelineEntry
	outputs map[uuid.UUID]string
	// next numbers the rows so each has a key of its own, the way the store's
	// row ids do.
	next int
}

// newDemoHistory seeds the timeline the demo page shows: the app's own
// problems, a channel line and one failed task per device.
func newDemoHistory(profiles []core.Profile) *demoHistory {
	base := time.Now().Add(-20 * time.Minute)
	h := &demoHistory{outputs: map[uuid.UUID]string{}}
	h.entries = append(h.entries,
		core.TimelineEntry{
			At: base.Add(19 * time.Minute), Level: core.LogWarn, Source: core.LogApp,
			Text: "未检测到系统凭据库，连接地址仅在本次运行中保留",
		},
		core.TimelineEntry{
			At: base.Add(8 * time.Minute), Level: core.LogError, Source: core.LogApp,
			Text: "上传失败：/sdcard/Download/update.zip 权限不足",
		},
	)
	for i, profile := range profiles {
		h.entries = append(h.entries, core.TimelineEntry{
			At: base.Add(time.Duration(10+i) * time.Minute), Level: core.LogInfo, Source: core.LogChannel,
			DeviceID: profile.ID, DeviceName: profile.Name,
			Text: "Tailcat 通道已就绪，本地 adb 入口 127.0.0.1:" + itoa(5556+i),
		})
		taskID := uuid.New()
		h.outputs[taskID] = "/sdcard/Download/update.zip: No such file or directory\n"
		h.entries = append(h.entries, core.TimelineEntry{
			At: base.Add(time.Duration(5+i) * time.Minute), Level: core.LogInfo, Source: core.LogTask,
			DeviceID: profile.ID, DeviceName: profile.Name, TaskID: taskID,
			Text: "adb pull /sdcard/Download/update.zip · 失败",
		})
	}
	sort.SliceStable(h.entries, func(i, j int) bool { return h.entries[i].At.After(h.entries[j].At) })
	h.rekeyLocked()
	return h
}

// rekeyLocked gives every row a key of its own, in timeline order.
func (h *demoHistory) rekeyLocked() {
	for i := range h.entries {
		h.next++
		h.entries[i].Key = "demo:" + itoa(h.next)
	}
}

// RecordTask, RecordLog and RecordAppLog keep what the demo produces while it
// runs, so a visitor's failed action appears on the page like the real thing.
func (h *demoHistory) RecordTask(record core.TaskRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := core.TimelineEntry{
		Key: "task:" + record.ID.String(),
		At:  record.Started, Level: core.LogInfo, Source: core.LogTask,
		DeviceID: record.DeviceID, DeviceName: record.DeviceName, TaskID: record.ID,
		Text: record.Title + " · " + record.State.Label(),
	}
	h.outputs[record.ID] = record.Output
	h.insert(entry)
	return nil
}

func (h *demoHistory) RecordLog(record core.LogRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.insert(core.TimelineEntry{
		At: record.At, Level: core.LogInfo, Source: core.LogChannel,
		DeviceID: record.DeviceID, Text: record.Text,
	})
	return nil
}

func (h *demoHistory) RecordAppLog(record core.AppLogRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	level := record.Level
	if level == "" {
		level = core.LogInfo
	}
	h.insert(core.TimelineEntry{At: record.At, Level: level, Source: core.LogApp, Text: record.Text})
	return nil
}

func (h *demoHistory) RecordReachability([]core.ReachabilityRecord) error { return nil }

func (h *demoHistory) RecentTasks(int) ([]core.Task, error) { return nil, nil }

func (h *demoHistory) Close() error { return nil }

// insert keeps the newest-first order without a sort on every write.
func (h *demoHistory) insert(entry core.TimelineEntry) {
	h.next++
	entry.Key = "demo:" + itoa(h.next)
	if entry.DeviceName == "" && entry.DeviceID != uuid.Nil {
		for _, existing := range h.entries {
			if existing.DeviceID == entry.DeviceID && existing.DeviceName != "" {
				entry.DeviceName = existing.DeviceName
				break
			}
		}
	}
	at := sort.Search(len(h.entries), func(i int) bool { return h.entries[i].At.Before(entry.At) })
	h.entries = append(h.entries, core.TimelineEntry{})
	copy(h.entries[at+1:], h.entries[at:])
	h.entries[at] = entry
}

// Timeline applies the same rules as the real store: device, level, since,
// every search term, then the limit.
func (h *demoHistory) Timeline(query core.LogQuery) ([]core.TimelineEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	limit := query.Limit
	if limit <= 0 {
		limit = core.DefaultLogLimit
	}
	terms := strings.Fields(query.Search)
	var out []core.TimelineEntry
	for _, entry := range h.entries {
		switch {
		case query.DeviceID == nil:
		case *query.DeviceID == uuid.Nil:
			if entry.DeviceID != uuid.Nil {
				continue
			}
		default:
			if entry.DeviceID != *query.DeviceID {
				continue
			}
		}
		if query.Level != "" && entry.Level != query.Level {
			continue
		}
		if !query.Since.IsZero() && entry.At.Before(query.Since) {
			continue
		}
		if !demoMatchesAll(append([]string{entry.Text}, demoTaskOutput(h, entry.TaskID)), terms) {
			continue
		}
		out = append(out, entry)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func demoTaskOutput(h *demoHistory, id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return h.outputs[id]
}

// demoMatchesAll reports whether every term appears in one of the fields.
func demoMatchesAll(fields []string, terms []string) bool {
	for _, term := range terms {
		found := false
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), strings.ToLower(term)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TaskOutput returns what the demo's task printed.
func (h *demoHistory) TaskOutput(id uuid.UUID) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	output, ok := h.outputs[id]
	if !ok {
		return "", core.Error(core.MsgTaskOutputMissing)
	}
	return output, nil
}

var (
	_ core.Recorder      = (*demoHistory)(nil)
	_ core.HistoryReader = (*demoHistory)(nil)
)

// ClearLogs drops the demo's app and channel lines in range, like the store.
func (h *demoHistory) ClearLogs(query core.LogQuery) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.entries[:0]
	var removed int64
	for _, entry := range h.entries {
		if entry.Source == core.LogTask {
			kept = append(kept, entry)
			continue
		}
		if query.DeviceID != nil && entry.DeviceID != *query.DeviceID {
			kept = append(kept, entry)
			continue
		}
		if !query.Since.IsZero() && entry.At.Before(query.Since) {
			kept = append(kept, entry)
			continue
		}
		removed++
	}
	h.entries = kept
	return removed, nil
}
