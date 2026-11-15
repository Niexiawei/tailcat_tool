package core

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func (f *managerFixture) connect(t *testing.T) string {
	t.Helper()
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	return f.m.Session(f.id).Serial
}

func TestBeginADBTaskBindsSnapshot(t *testing.T) {
	f := newManagerFixture(t)
	serial := f.connect(t)

	taskID, err := f.m.BeginADBTask(context.Background(), f.id, "上传 x", []string{"push", "/tmp/a", "/sdcard/a"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	f.m.WaitTask(taskID)

	var push []string
	for _, cmd := range f.runner.commandLog() {
		if strings.Contains(cmd, " push ") {
			push = append(push, cmd)
		}
	}
	if len(push) != 1 {
		t.Fatalf("push ran %d times, want 1 (%v)", len(push), f.runner.commandLog())
	}
	if !strings.Contains(push[0], "-s "+serial+" push /tmp/a /sdcard/a") {
		t.Fatalf("push command = %q, want the captured serial %s", push[0], serial)
	}
	tasks := f.m.Tasks()
	if len(tasks) != 1 || tasks[0].Serial != serial || tasks[0].State != TaskSucceeded {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestTaskOutputCapAndRedaction(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	big := strings.Repeat("x", 40000) + " " + sampleAddress
	f.runner.setHandler(func(ctx context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return Result{Output: big}, nil
	})
	taskID, err := f.m.BeginADBTask(context.Background(), f.id, "shell", []string{"shell", "echo"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	f.m.WaitTask(taskID)
	tasks := f.m.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	out := tasks[0].Output
	if len(out) > 32768 {
		t.Fatalf("task output = %d bytes, want at most 32768", len(out))
	}
	if strings.Contains(out, sampleAddress) {
		t.Fatal("a credential survived into the task output")
	}
}

func TestTaskHistoryCap(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	for i := 0; i < 105; i++ {
		taskID, err := f.m.BeginADBTask(context.Background(), f.id, "noop", []string{"shell", "true"}, time.Minute)
		if err != nil {
			t.Fatalf("BeginADBTask() error: %v", err)
		}
		f.m.WaitTask(taskID)
	}
	if got := len(f.m.Tasks()); got > 100 {
		t.Fatalf("history holds %d tasks, want at most 100", got)
	}
}

func TestScreenUsesScrcpyArgs(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.Configuration.Tools.Scrcpy = os.Args[0]
	})
	f.connect(t)
	taskID, err := f.m.BeginScreen(context.Background(), f.id)
	if err != nil {
		t.Fatalf("BeginScreen() error: %v", err)
	}
	f.m.WaitTask(taskID)

	var screen *Command
	f.runner.mu.Lock()
	for i := range f.runner.calls {
		if strings.Contains(f.runner.calls[i].Path, ".") && contains(f.runner.calls[i].Args, "--max-size=1280") {
			screen = &f.runner.calls[i]
		}
	}
	f.runner.mu.Unlock()
	if screen == nil {
		t.Fatalf("no scrcpy command in %v", f.runner.commandLog())
	}
	for _, want := range []string{"--max-size=1280", "--video-bit-rate=4M", "--max-fps=30"} {
		if !contains(screen.Args, want) {
			t.Fatalf("scrcpy args %v lack %q", screen.Args, want)
		}
	}
	if screen.Timeout != 24*time.Hour {
		t.Fatalf("scrcpy timeout = %v, want 24h", screen.Timeout)
	}
	foundADBEnv := false
	for _, env := range screen.Env {
		if strings.HasPrefix(env, "ADB=") {
			foundADBEnv = true
		}
	}
	if !foundADBEnv {
		t.Fatalf("scrcpy env %v lacks ADB=", screen.Env)
	}
}

func TestValidateTargetRejectsOffline(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(ctx context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device offline\n"}, nil
		}
		return Result{}, nil
	})
	_, err := f.m.BeginADBTask(context.Background(), f.id, "shell", []string{"shell", "true"}, time.Minute)
	if err == nil {
		t.Fatal("BeginADBTask() accepted an offline target")
	}
	if !strings.Contains(err.Error(), "离线") {
		t.Fatalf("error = %v, want the offline wording", err)
	}
}

func TestDiagnoseSetsRoute(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	pinger := &Pinger{
		Identities: f.m.identities,
		Dialer: &fakePingDialer{newFunc: func() *fakePingSession {
			return &fakePingSession{result: PingResult{Latency: 12 * time.Millisecond, Direct: true, Route: "直连 · 1.2.3.4:41641"}}
		}},
	}
	f.m.pinger = pinger

	if err := f.m.Diagnose(context.Background(), f.id); err != nil {
		t.Fatalf("Diagnose() error: %v", err)
	}
	if got := f.m.Session(f.id).Route; got != "直连 · 1.2.3.4:41641" {
		t.Fatalf("route = %q", got)
	}
}

func TestRefreshDeviceInformation(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(ctx context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		for _, arg := range c.Args {
			if strings.Contains(arg, "getprop ro.product.model") {
				return Result{Output: "E245\nantora\n11\nSN123\nuid=0(root)\n"}, nil
			}
		}
		return Result{}, nil
	})
	if err := f.m.RefreshDeviceInformation(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshDeviceInformation() error: %v", err)
	}
	session := f.m.Session(f.id)
	if session.Model != "E245" || session.Product != "antora" || session.Android != "11" ||
		session.DeviceSerial != "SN123" || session.ShellID != "uid=0(root)" {
		t.Fatalf("session = %+v", session)
	}
}

// Reconnecting must cancel the tasks of the previous channel.
func TestReconnectCancelsOldTasks(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	release := make(chan struct{})
	var once sync.Once
	f.runner.setHandler(func(ctx context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if contains(c.Args, "sleep 30") {
			once.Do(func() { close(release) })
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
			}
		}
		return Result{}, nil
	})
	taskID, err := f.m.BeginADBTask(context.Background(), f.id, "slow", []string{"shell", "sleep 30"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	<-release
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("reconnect error: %v", err)
	}
	f.m.WaitTask(taskID)
	for _, task := range f.m.Tasks() {
		if task.ID == taskID && task.State == TaskRunning {
			t.Fatal("the old task survived a reconnect")
		}
	}
}
