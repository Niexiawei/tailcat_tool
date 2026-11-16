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
}

func (f *fakeFileRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in appmeta tests")
}

func (f *fakeFileRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	f.record(c)
	line := strings.Join(c.Args, " ")
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

func TestStoreBacksOffAfterAFailedFetch(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	// No APK path: the device answers "unknown package", as it does for
	// processes that are not applications at all.
	runner := &fakeFileRunner{}
	s := newTestStore(t, runner)
	s.now = func() time.Time { return now }

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("first attempt ran %d pm path calls", got)
	}

	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
	s.waitIdle()
	if got := runner.count("pm path"); got != 1 {
		t.Fatalf("a failed package was retried immediately (%d calls)", got)
	}

	now = now.Add(11 * time.Minute)
	s.Request("dev", "/usr/bin/adb", "emulator-5554", []string{"system_server"})
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
