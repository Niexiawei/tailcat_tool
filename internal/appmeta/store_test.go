package appmeta

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
)

// fakeFileRunner answers the two adb calls the store makes and hands the
// payload to RunToFile, so no device is involved.
type fakeFileRunner struct {
	mu      sync.Mutex
	apkPath string
	apkSize string
	payload []byte
	// entries answers `unzip -p`: entry name to its bytes.
	entries map[string][]byte
	calls   []string
	// holdOn blocks the call whose arguments contain it until gate closes, so a
	// test can look at the store's state while a read is running.
	holdOn string
	gate   chan struct{}
	held   chan struct{}
	// panicOn makes the worker explode, to prove a panic still ends the row's
	// pending state.
	panicOn string
	// failOn makes the call return an error, the way a dropped adb channel does.
	failOn  string
	failErr error
}

func (f *fakeFileRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in appmeta tests")
}

func (f *fakeFileRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	f.record(c)
	line := strings.Join(c.Args, " ")
	if f.panicOn != "" && strings.Contains(line, f.panicOn) {
		panic("the fake runner exploded")
	}
	if f.failOn != "" && strings.Contains(line, f.failOn) {
		return core.Result{ExitCode: 1}, f.failErr
	}
	if f.gate != nil && f.holdOn != "" && strings.Contains(line, f.holdOn) {
		close(f.held)
		<-f.gate
	}
	switch {
	case strings.Contains(line, "pm path"):
		if f.apkPath == "" {
			return core.Result{ExitCode: 1, Output: "unknown package"}, nil
		}
		return core.Result{Output: "package:" + f.apkPath + "\n"}, nil
	case strings.Contains(line, "stat -c"):
		if f.apkSize == "" {
			return core.Result{ExitCode: 1}, nil
		}
		return core.Result{Output: f.apkSize + "\n"}, nil
	}
	return core.Result{}, nil
}

func (f *fakeFileRunner) RunToFile(_ context.Context, c core.Command, path string) (core.Result, error) {
	f.record(c)
	line := strings.Join(c.Args, " ")
	if i := strings.Index(line, "unzip -p"); i >= 0 {
		fields := strings.Fields(line[i:])
		if len(fields) < 4 {
			return core.Result{ExitCode: 1}, nil
		}
		data, ok := f.entries[strings.Trim(fields[3], "'")]
		if !ok {
			return core.Result{ExitCode: 11}, nil
		}
		return core.Result{}, os.WriteFile(path, data, 0o600)
	}
	if f.payload == nil {
		return core.Result{ExitCode: 1}, nil
	}
	return core.Result{}, os.WriteFile(path, f.payload, 0o600)
}

// zipEntries opens the fixture APK and returns its entries by name, so the fake
// `unzip -p` can answer exactly what a device would.
func zipEntries(t *testing.T, apk []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(apk), int64(len(apk)))
	if err != nil {
		t.Fatalf("opening the test apk: %v", err)
	}
	out := map[string][]byte{}
	for _, file := range reader.File {
		f, err := file.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", file.Name, err)
		}
		out[file.Name] = data
	}
	return out
}

func (f *fakeFileRunner) record(c core.Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(c.Args, " "))
}

func (f *fakeFileRunner) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeFileRunner) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeFileRunner) count(needle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if strings.Contains(call, needle) {
			n++
		}
	}
	return n
}

func newTestStore(t *testing.T, runner core.FileRunner) *Store {
	t.Helper()
	s, err := New(t.TempDir(), runner, 1)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return s
}

// apkRunner serves one APK for any package it is asked about, both whole and
// entry by entry.
func apkRunner(apk []byte, size string) *fakeFileRunner {
	return &fakeFileRunner{
		apkPath: "/data/app/com.foo-1/base.apk",
		apkSize: size,
		payload: apk,
		entries: nil,
	}
}

func TestStoreReadsLabelAndIconFromApk(t *testing.T) {
	apk := buildTestAPK(t, "微信", "res/mipmap-xxhdpi/ic.png", testPNG)
	runner := apkRunner(apk, "4096 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	meta, ok := s.Lookup("dev", "com.foo")
	if !ok {
		t.Fatal("Lookup() knows nothing after a successful fetch")
	}
	if meta.Label != "微信" {
		t.Fatalf("Label = %q, want 微信", meta.Label)
	}
	if !bytes.Equal(meta.Icon, testPNG) {
		t.Fatalf("Icon = %d bytes, want the %d-byte PNG", len(meta.Icon), len(testPNG))
	}
	if runner.count("exec-out cat") != 1 {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestStoreSkipsFreshEntryAndRefetchesStale(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	runner := apkRunner(buildTestAPK(t, "微信", "", nil), "4096 1700000000")
	s := newTestStore(t, runner)
	s.now = func() time.Time { return now }

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	runner.reset()

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 0 {
		t.Fatalf("a fresh entry was fetched again (%d pm path calls)", got)
	}

	now = now.Add(TTL + time.Hour)
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("a stale entry was not refetched (%d pm path calls)", got)
	}
}

func TestStoreRefreshBypassesTTL(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	runner := apkRunner(buildTestAPK(t, "旧名字", "", nil), "4096 1700000000")
	s := newTestStore(t, runner)
	s.now = func() time.Time { return now }

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	runner.reset()

	// The APK changed on the device; a refresh must read it again even though
	// the cached entry is only minutes old.
	runner.mu.Lock()
	runner.payload = buildTestAPK(t, "新名字", "", nil)
	runner.mu.Unlock()
	s.Refresh("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("refresh skipped the read (%d pm path calls)", got)
	}
	meta, ok := s.Lookup("dev", "com.foo")
	if !ok || meta.Label != "新名字" {
		t.Fatalf("meta = %+v, ok=%v", meta, ok)
	}
}

func TestStoreFallsBackWhenApkIsCorrupt(t *testing.T) {
	runner := apkRunner([]byte("this is not a zip file"), "4096 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if meta, ok := s.Lookup("dev", "com.foo"); ok {
		t.Fatalf("a corrupt APK produced %+v", meta)
	}
}

func TestStoreRejectsOversizedApk(t *testing.T) {
	runner := apkRunner(buildTestAPK(t, "微信", "", nil), "500000000 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if got := runner.count("exec-out cat"); got != 0 {
		t.Fatalf("pulled a %d-byte APK %d times", 500000000, got)
	}
	if _, ok := s.Lookup("dev", "com.foo"); ok {
		t.Fatal("an oversized APK produced metadata")
	}
}

func TestStoreIgnoresDuplicateRequests(t *testing.T) {
	runner := apkRunner(buildTestAPK(t, "微信", "", nil), "4096 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo", "com.foo"})
	s.waitIdle()

	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("the same package was queued %d times", got)
	}
}

func TestStoreRetriesAFailedFetchAfterTheBackoff(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	// A dropped channel: the command itself fails, which is worth retrying.
	runner := apkRunner(buildTestAPK(t, "微信", "", nil), "4096 1700000000")
	runner.failOn = "pm path"
	runner.failErr = core.Error("adb: device offline")
	s := newTestStore(t, runner)
	s.now = func() time.Time { return now }

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("first attempt ran %d pm path calls", got)
	}
	status, ok := s.Status("dev", "com.foo")
	if !ok || status.State != core.AppMetaFailed || status.Final {
		t.Fatalf("status = %+v, ok=%v; want a retryable failure", status, ok)
	}

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("a failed package was retried immediately (%d calls)", got)
	}

	now = now.Add(11 * time.Minute)
	runner.mu.Lock()
	runner.failOn = ""
	runner.mu.Unlock()
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 2 {
		t.Fatalf("the backoff never expired (%d calls)", got)
	}
}

func TestStoreKeepsLabelWhenIconIsVector(t *testing.T) {
	vector := []byte(`<vector xmlns:android="http://schemas.android.com/apk/res/android"/>`)
	apk := buildTestAPK(t, "微信", "res/mipmap-anydpi-v26/ic.xml", vector)
	s := newTestStore(t, apkRunner(apk, "4096 1700000000"))

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	meta, ok := s.Lookup("dev", "com.foo")
	if !ok || meta.Label != "微信" {
		t.Fatalf("meta = %+v, ok=%v", meta, ok)
	}
	if len(meta.Icon) != 0 {
		t.Fatalf("a vector icon became %d bytes", len(meta.Icon))
	}
}

func TestStoreReadsEntriesInsteadOfTheWholeApk(t *testing.T) {
	apk := buildTestAPK(t, "微信", "res/mipmap-xxhdpi/ic.png", testPNG)
	runner := apkRunner(apk, "4096 1700000000")
	runner.entries = zipEntries(t, apk)
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	meta, ok := s.Lookup("dev", "com.foo")
	if !ok || meta.Label != "微信" {
		t.Fatalf("meta = %+v, ok=%v", meta, ok)
	}
	if !bytes.Equal(meta.Icon, testPNG) {
		t.Fatalf("Icon = %d bytes, want the fixture PNG", len(meta.Icon))
	}
	if got := runner.count("exec-out cat"); got != 0 {
		t.Fatalf("pulled the whole APK %d times instead of its entries", got)
	}
	if got := runner.count("unzip -p"); got < 2 {
		t.Fatalf("unzip calls = %d, want the manifest and the icon", got)
	}
}

func TestStoreFallsBackToTheWholeApkWithoutUnzip(t *testing.T) {
	apk := buildTestAPK(t, "微信", "res/mipmap-xxhdpi/ic.png", testPNG)
	// No entries map: `unzip -p` fails the way a device without unzip does.
	runner := apkRunner(apk, "4096 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if meta, ok := s.Lookup("dev", "com.foo"); !ok || meta.Label != "微信" {
		t.Fatalf("meta = %+v, ok=%v", meta, ok)
	}
	if got := runner.count("exec-out cat"); got != 1 {
		t.Fatalf("whole-APK pulls = %d, want the fallback to run once", got)
	}
}

func TestStoreReadsEntriesThroughExecOut(t *testing.T) {
	apk := buildTestAPK(t, "微信", "res/mipmap-xxhdpi/ic.png", testPNG)
	runner := apkRunner(apk, "4096 1700000000")
	runner.entries = zipEntries(t, apk)
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if meta, ok := s.Lookup("dev", "com.foo"); !ok || meta.Label != "微信" {
		t.Fatalf("meta = %+v, ok=%v", meta, ok)
	}
	// `adb shell` gives the remote command a PTY, and the tty rewrites every LF
	// into CRLF: the emulator's 724620-byte manifest arrives as 739233 bytes and
	// the parser then fails for every APK. Binary entries must use exec-out.
	for _, call := range runner.callsSnapshot() {
		if strings.Contains(call, "shell unzip") {
			t.Fatalf("an entry was read through adb shell, whose PTY corrupts it: %q", call)
		}
	}
	if got := runner.count("exec-out unzip"); got < 2 {
		t.Fatalf("exec-out entry reads = %d, want the manifest and the icon", got)
	}
}

func TestStoreReportsNoAppPackageAsFinalFailure(t *testing.T) {
	// The device answers `pm path` with exit 1 and no `package:` line, exactly
	// as it does for a system service that is not an installed application.
	runner := &fakeFileRunner{}
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"android.hardware.audio.service"})
	s.waitIdle()

	status, ok := s.Status("dev", "android.hardware.audio.service")
	if !ok || status.State != core.AppMetaFailed {
		t.Fatalf("status = %+v, ok=%v", status, ok)
	}
	if !status.Final || status.Reason != core.ReasonNoAppPackage {
		t.Fatalf("status = %+v; a process with no APK is a final failure with a reason", status)
	}
}

func TestStoreReportsPendingWhileReading(t *testing.T) {
	apk := buildTestAPK(t, "微信", "", nil)
	runner := apkRunner(apk, "4096 1700000000")
	runner.entries = zipEntries(t, apk)
	runner.holdOn = "pm path com.foo"
	runner.gate = make(chan struct{})
	runner.held = make(chan struct{})
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	<-runner.held
	if status, ok := s.Status("dev", "com.foo"); !ok || status.State != core.AppMetaPending {
		t.Fatalf("status = %+v, ok=%v; a running read must read as pending", status, ok)
	}
	close(runner.gate)
	s.waitIdle()
	if status, ok := s.Status("dev", "com.foo"); !ok || status.State != core.AppMetaReady {
		t.Fatalf("status = %+v, ok=%v; want ready once the read finished", status, ok)
	}
}

func TestStoreSkipsPackagesKnownToHaveNoAppPackage(t *testing.T) {
	runner := &fakeFileRunner{}
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("first attempt ran %d pm path calls", got)
	}

	// The batch path queues it again; the store already settled the answer, so
	// it must not spend another round trip on it.
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("a settled package was asked again (%d pm path calls)", got)
	}

	// A row's retry is the user insisting, so it may ask once more.
	s.Refresh("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 2 {
		t.Fatalf("an explicit retry did not ask again (%d pm path calls)", got)
	}
}

func TestStoreGivesUpOnAPKsTooLargeToPull(t *testing.T) {
	// 80 MB is below the parse ceiling and above what is worth downloading over
	// a channel. No entries map, so the fast path fails and the fallback would
	// have to pull the whole package.
	runner := apkRunner(buildTestAPK(t, "微信", "", nil), "83886080 1700000000")
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	status, ok := s.Status("dev", "com.foo")
	if !ok || status.State != core.AppMetaFailed || status.Reason != core.ReasonAPKTooLarge {
		t.Fatalf("status = %+v, ok=%v; want a stated \"too large\" failure", status, ok)
	}
	if got := runner.count("exec-out cat"); got != 0 {
		t.Fatalf("pulled an 80 MB APK %d times instead of giving up", got)
	}
}

func TestStoreServesRowRequestsBeforeTheBatch(t *testing.T) {
	apk := buildTestAPK(t, "微信", "", nil)
	runner := apkRunner(apk, "4096 1700000000")
	runner.entries = zipEntries(t, apk)
	runner.holdOn = "pm path ba"
	runner.gate = make(chan struct{})
	runner.held = make(chan struct{})
	s := newTestStore(t, runner)

	// A batch refresh is already running...
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"ba", "bb", "bc", "bd", "be"})
	<-runner.held
	// ...and the user clicks one row.
	s.Refresh("dev", "/usr/bin/adb", "emulator-5554", []string{"row"})
	close(runner.gate)
	s.waitIdle()

	calls := runner.callsSnapshot()
	row, next := -1, -1
	for i, call := range calls {
		if row < 0 && strings.Contains(call, "pm path row") {
			row = i
		}
		if next < 0 && strings.Contains(call, "pm path bb") {
			next = i
		}
	}
	if row < 0 {
		t.Fatalf("the row was never read: %v", calls)
	}
	if next >= 0 && row > next {
		t.Fatalf("the row was read as call %d, after the batch's call %d: %v", row, next, calls)
	}
}

func TestStoreMarksAFailedStateWhenTheWorkerPanics(t *testing.T) {
	apk := buildTestAPK(t, "微信", "", nil)
	runner := apkRunner(apk, "4096 1700000000")
	runner.panicOn = "pm path"
	s := newTestStore(t, runner)

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()

	if status, ok := s.Status("dev", "com.foo"); !ok || status.State != core.AppMetaFailed {
		t.Fatalf("status = %+v, ok=%v; a panicking read must still settle the row", status, ok)
	}

	// The worker survived the panic and still serves the next request.
	runner.panicOn = ""
	s.Refresh("dev", "/usr/bin/adb", "emulator-5554", []string{"com.foo"})
	s.waitIdle()
	if status, ok := s.Status("dev", "com.foo"); !ok || status.State != core.AppMetaReady {
		t.Fatalf("status = %+v, ok=%v; the worker died with the panic", status, ok)
	}
}
