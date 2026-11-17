package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// listingFixture is what a healthy device prints for /sdcard.
const listingFixture = "directory|4096|770|drwxrwx---|1700000000|./Download\n" +
	"regular file|12|660|-rw-rw----|1700000000|./照片.jpg\n"

// countingRecorder keeps what the manager recorded, so a test can prove that an
// action recorded nothing.
type countingRecorder struct {
	mu    sync.Mutex
	tasks []TaskRecord
	logs  []LogRecord
}

func (r *countingRecorder) RecordTask(t TaskRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, t)
	return nil
}

func (r *countingRecorder) RecordLog(l LogRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, l)
	return nil
}

func (r *countingRecorder) RecordReachability([]ReachabilityRecord) error { return nil }

func (r *countingRecorder) RecentTasks(int) ([]Task, error) { return nil, nil }

func (r *countingRecorder) Close() error { return nil }

func (r *countingRecorder) taskCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tasks)
}

// listFixture answers get-state and returns fixture for the listing script.
func listFixture(fixture string) func(context.Context, Command) (Result, error) {
	return func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if last := c.Args[len(c.Args)-1]; strings.Contains(last, "stat -c") {
			return Result{Output: fixture}, nil
		}
		return Result{}, nil
	}
}

func TestListRemoteDirCachesTheListing(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(listFixture(listingFixture))
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	listing, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard")
	if err != nil {
		t.Fatalf("ListRemoteDir() error: %v", err)
	}
	if listing.Path != "/sdcard" || len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v", listing)
	}
	// Directories come first, whatever order the device printed.
	if !listing.Entries[0].Dir || listing.Entries[0].Name != "Download" {
		t.Fatalf("first entry = %+v", listing.Entries[0])
	}
	if listing.Entries[1].Name != "照片.jpg" || listing.Entries[1].Dir {
		t.Fatalf("second entry = %+v", listing.Entries[1])
	}

	cached, ok := f.m.DirListing(f.id, "/sdcard")
	if !ok || len(cached.Entries) != 2 {
		t.Fatalf("cached = %+v ok = %v", cached, ok)
	}
}

func TestListRemoteDirCreatesNoTask(t *testing.T) {
	recorder := &countingRecorder{}
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Recorder = recorder })
	f.runner.setHandler(listFixture(listingFixture))
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard"); err != nil {
		t.Fatalf("ListRemoteDir() error: %v", err)
	}
	if tasks := f.m.Tasks(); len(tasks) != 0 {
		t.Fatalf("reading a directory created %d tasks: %+v", len(tasks), tasks)
	}
	if n := recorder.taskCount(); n != 0 {
		t.Fatalf("reading a directory recorded %d tasks in history", n)
	}
}

func TestListRemoteDirSaysWhenTheDirectoryIsMissing(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") || len(c.Args) == 3 {
			return Result{Output: "device\n"}, nil
		}
		return Result{ExitCode: 9}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard/nope")
	if err == nil {
		t.Fatal("a missing directory must fail")
	}
	if want := "目录不存在或不可读：/sdcard/nope"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if _, ok := f.m.DirListing(f.id, "/sdcard/nope"); ok {
		t.Fatal("a failed read must not be cached")
	}
}

func TestListRemoteDirRefusesAPathWithANewline(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(listFixture(listingFixture))
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	before := len(f.runner.commandLog())

	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard/a\nb"); err == nil {
		t.Fatal("a path with a newline must be refused")
	}
	if after := len(f.runner.commandLog()); after != before {
		t.Fatalf("adb was called %d more times for an unusable path", after-before)
	}
}

func TestListRemoteDirRefusesAnOfflineDevice(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(listFixture(listingFixture))

	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard"); err == nil {
		t.Fatal("an unconnected device must fail")
	}
}

func TestListRemoteDirIsForgottenOnDisconnect(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(listFixture(listingFixture))
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard"); err != nil {
		t.Fatalf("ListRemoteDir() error: %v", err)
	}

	f.m.Disconnect(context.Background(), f.id)

	if _, ok := f.m.DirListing(f.id, "/sdcard"); ok {
		t.Fatal("a listing outlived its channel")
	}
}

func TestListRemoteDirKeepsOtherDevicesListings(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(listFixture(listingFixture))
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	other := uuid.New()
	f.m.dirListings[dirKey{device: other, path: "/sdcard"}] = DirListing{Path: "/sdcard"}
	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard"); err != nil {
		t.Fatalf("ListRemoteDir() error: %v", err)
	}

	f.m.Disconnect(context.Background(), f.id)

	if _, ok := f.m.DirListing(other, "/sdcard"); !ok {
		t.Fatal("disconnecting one device dropped another device's listing")
	}
}

// TestListRemoteDirTimeoutSaysWhatHappened keeps the timeout from looking like
// an empty folder, which would read as "these files are gone".
func TestListRemoteDirTimeoutSaysWhatHappened(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") || len(c.Args) == 3 {
			return Result{Output: "device\n"}, nil
		}
		return Result{TimedOut: true, ExitCode: -1}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard")
	if err == nil || err.Error() != "读取目录超时，请重试" {
		t.Fatalf("error = %v", err)
	}
}

// TestListRemoteDirExplainsAMissingPermission turns the shell's own words into
// what the user actually needs: which folder, and why. /cache on Android is the
// everyday example — it belongs to root, not to the shell user.
func TestListRemoteDirExplainsAMissingPermission(t *testing.T) {
	f := newManagerFixture(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return Result{ExitCode: 9, Output: "/system/bin/sh: cd: /cache: Permission denied\n"}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, err := f.m.ListRemoteDir(context.Background(), f.id, "/cache")
	if err == nil || err.Error() != "没有权限查看该文件夹：/cache" {
		t.Fatalf("error = %v", err)
	}
}
