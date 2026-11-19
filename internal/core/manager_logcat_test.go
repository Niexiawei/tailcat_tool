package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// connectedLogcatFixture is a manager with one device online and its streaming
// commands captured instead of run.
func connectedLogcatFixture(t *testing.T, opts ...func(*ManagerOptions)) *managerFixture {
	t.Helper()
	f := newManagerFixture(t, opts...)
	// A stream whose process never prints and never ends, unless a test says
	// otherwise. PID 0 keeps Proc.Stop from touching anything real.
	f.runner.setStartHandler(func(context.Context, Command, func(string)) (*Proc, error) {
		return &Proc{Done: make(chan int, 1)}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { f.m.Disconnect(context.Background(), f.id) })
	return f
}

// logcatStarts keeps only the streaming commands, so a test does not have to
// know how many one-shot adb calls a connect made.
func logcatStarts(f *managerFixture) [][]string {
	var out [][]string
	for _, args := range f.runner.startLog() {
		if len(args) > 2 && args[2] == "logcat" {
			out = append(out, args)
		}
	}
	return out
}

// waitForLogcat polls until want says the snapshot is what the test expects.
func waitForLogcat(t *testing.T, stream *LogcatStream, want func(LogcatSnapshot) bool) LogcatSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := stream.Snapshot(LogcatFilter{})
		if want(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream never reached the expected state; last snapshot: %+v", snap)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStartLogcatRunsThreadtimeOnTheDevice(t *testing.T) {
	f := connectedLogcatFixture(t)
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	t.Cleanup(func() { f.m.StopLogcat(f.id) })
	starts := logcatStarts(f)
	if len(starts) != 1 {
		t.Fatalf("started %d streams, want 1: %v", len(starts), starts)
	}
	serial := f.m.Session(f.id).Serial
	want := "-s " + serial + " logcat -v threadtime"
	if got := strings.Join(starts[0], " "); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if stream.Closed() {
		t.Fatal("a fresh stream reports itself closed")
	}
}

func TestStartLogcatHonoursTheBufferSelection(t *testing.T) {
	f := connectedLogcatFixture(t)
	if _, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{Buffers: []string{"main", "crash"}}); err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	t.Cleanup(func() { f.m.StopLogcat(f.id) })
	args := logcatStarts(f)[0]
	if got := args[len(args)-2] + " " + args[len(args)-1]; got != "-b main,crash" {
		t.Fatalf("the command does not select the buffers: %v", args)
	}
}

func TestStartLogcatRefusesADeviceThatIsNotOnline(t *testing.T) {
	f := newManagerFixture(t)
	if _, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{}); err == nil {
		t.Fatal("StartLogcat() succeeded on a device that is not connected")
	}
	if starts := logcatStarts(f); len(starts) != 0 {
		t.Fatalf("started adb anyway: %v", starts)
	}
}

func TestStartLogcatReplacesTheDeviceStream(t *testing.T) {
	f := connectedLogcatFixture(t)
	first, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("first StartLogcat() error: %v", err)
	}
	second, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("second StartLogcat() error: %v", err)
	}
	t.Cleanup(func() { f.m.StopLogcat(f.id) })
	if !first.Closed() {
		t.Fatal("the previous stream is still open: two logcats on one device")
	}
	if second.Closed() {
		t.Fatal("the new stream was closed too")
	}
}

func TestLogcatStreamParsesWhatTheDevicePrints(t *testing.T) {
	var feed func(string)
	f := connectedLogcatFixture(t)
	f.runner.setStartHandler(func(_ context.Context, _ Command, onOutput func(string)) (*Proc, error) {
		feed = onOutput
		return &Proc{Done: make(chan int, 1)}, nil
	})
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	t.Cleanup(func() { f.m.StopLogcat(f.id) })
	if feed == nil {
		t.Fatal("StartLogcat did not connect the pipe to the stream")
	}
	feed("11-18 09:30:01.123  1234  1250 I ActivityManager: first\n")
	feed("11-18 09:30:02.000  1234  1250 W ActivityManager: sec")
	feed("ond\n")
	snap := waitForLogcat(t, stream, func(s LogcatSnapshot) bool { return len(s.Entries) == 2 })
	if snap.Entries[0].Text != "first" || snap.Entries[1].Text != "second" {
		t.Fatalf("entries = %+v, want the two lines the pipe carried", snap.Entries)
	}
}

func TestLogcatStreamReportsAnExitedProcess(t *testing.T) {
	done := make(chan int, 1)
	f := connectedLogcatFixture(t)
	f.runner.setStartHandler(func(context.Context, Command, func(string)) (*Proc, error) {
		return &Proc{Done: done}, nil
	})
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	t.Cleanup(func() { f.m.StopLogcat(f.id) })
	done <- 1
	snap := waitForLogcat(t, stream, func(s LogcatSnapshot) bool { return s.Exited })
	if snap.Err == "" {
		t.Fatal("a logcat that exited with 1 reports no reason")
	}
}

func TestDisconnectStopsTheDeviceLogcatStream(t *testing.T) {
	f := connectedLogcatFixture(t)
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	f.m.Disconnect(context.Background(), f.id)
	if !stream.Closed() {
		t.Fatal("the stream outlived its device's channel")
	}
}

func TestStopLogcatIsIdempotent(t *testing.T) {
	f := connectedLogcatFixture(t)
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	f.m.StopLogcat(f.id)
	f.m.StopLogcat(f.id)
	f.m.StopLogcat(uuid.Nil)
	if !stream.Closed() {
		t.Fatal("StopLogcat did not close the stream")
	}
}

func TestShutdownWaitsForTheLogcatStop(t *testing.T) {
	f := connectedLogcatFixture(t)
	stream, err := f.m.StartLogcat(context.Background(), f.id, LogcatOptions{})
	if err != nil {
		t.Fatalf("StartLogcat() error: %v", err)
	}
	// A stop costs most of a second on Windows; quitting the app cannot leave
	// that to a goroutine, or the adb outlives the app.
	stopped := make(chan struct{})
	stream.attach(func() {
		time.Sleep(30 * time.Millisecond)
		close(stopped)
	}, func() {})
	f.m.Shutdown(context.Background())
	select {
	case <-stopped:
	default:
		t.Fatal("Shutdown returned before the logcat process was stopped")
	}
	if !stream.Closed() {
		t.Fatal("Shutdown left the stream open")
	}
}
