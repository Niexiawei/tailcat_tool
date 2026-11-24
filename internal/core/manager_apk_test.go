package core

import (
	"context"
	"strings"
	"testing"
)

func appAPKFixture(t *testing.T, handler func(c Command) (Result, error)) *managerFixture {
	t.Helper()
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return handler(c)
	})
	return f
}

func TestAppAPKReadsTheBasePathFromThePackageListing(t *testing.T) {
	f := appAPKFixture(t, func(c Command) (Result, error) {
		switch {
		case containsSubstring(c.Args, "pm list packages"):
			return Result{Output: "package:/data/app/~~x==/com.foo-y==/base.apk=com.foo\n"}, nil
		case containsSubstring(c.Args, "stat -c"):
			return Result{Output: "1234\n"}, nil
		}
		return Result{}, nil
	})
	apk, err := f.m.AppAPK(context.Background(), f.id, "com.foo")
	if err != nil {
		t.Fatalf("AppAPK() error: %v", err)
	}
	if apk.Path != "/data/app/~~x==/com.foo-y==/base.apk" {
		t.Fatalf("AppAPK().Path = %q", apk.Path)
	}
	if apk.Size != 1234 {
		t.Fatalf("AppAPK().Size = %d", apk.Size)
	}
}

// The listing spans every user, so it sees applications pm path cannot. When
// the device does not support it, the older lookup is still worth a try.
func TestAppAPKFallsBackToPMPathWhenTheListingFails(t *testing.T) {
	f := appAPKFixture(t, func(c Command) (Result, error) {
		switch {
		case containsSubstring(c.Args, "pm list packages"):
			return Result{Output: "Error: unknown option --user\n", ExitCode: 1}, nil
		case containsSubstring(c.Args, "pm path"):
			return Result{Output: "package:/data/app/com.foo/base.apk\n"}, nil
		case containsSubstring(c.Args, "stat -c"):
			return Result{Output: "2048\n"}, nil
		}
		return Result{}, nil
	})
	apk, err := f.m.AppAPK(context.Background(), f.id, "com.foo")
	if err != nil {
		t.Fatalf("AppAPK() error: %v", err)
	}
	if apk.Path != "/data/app/com.foo/base.apk" || apk.Size != 2048 {
		t.Fatalf("AppAPK() = %+v", apk)
	}
}

func TestAppAPKReportsAMissingPackage(t *testing.T) {
	f := appAPKFixture(t, func(c Command) (Result, error) {
		if containsSubstring(c.Args, "pm list packages") {
			return Result{Output: "package:/data/app/com.other/base.apk=com.other\n"}, nil
		}
		return Result{}, nil
	})
	_, err := f.m.AppAPK(context.Background(), f.id, "com.foo")
	if err == nil || err.Error() != MsgAPKPathMissing {
		t.Fatalf("AppAPK() error = %v, want %q", err, MsgAPKPathMissing)
	}
}

// The package name is pasted into an `adb shell` line and it comes from the
// device's own /proc, so a name that could carry a second command is refused
// before any adb runs at all.
func TestAppAPKRejectsAPackageNameThatCouldRunACommand(t *testing.T) {
	f := appAPKFixture(t, func(Command) (Result, error) { return Result{}, nil })
	for _, pkg := range []string{"com.foo; reboot", "com.foo$(id)", "com.foo\nid", "com.foo|id", "'", " "} {
		if _, err := f.m.AppAPK(context.Background(), f.id, pkg); err == nil {
			t.Fatalf("AppAPK(%q) was accepted", pkg)
		}
	}
	for _, call := range f.runner.commandLog() {
		if strings.Contains(call, "pm list packages") || strings.Contains(call, "stat -c") || strings.Contains(call, "reboot") {
			t.Fatalf("a refused package name still reached adb: %q", call)
		}
	}
}

func TestAppAPKRequiresAnOnlineDevice(t *testing.T) {
	f := newManagerFixture(t)
	if _, err := f.m.AppAPK(context.Background(), f.id, "com.foo"); err == nil {
		t.Fatal("AppAPK() accepted a device with no channel")
	}
	for _, call := range f.runner.commandLog() {
		if strings.Contains(call, "pm") {
			t.Fatalf("an offline read touched adb: %q", call)
		}
	}
}

// A device that will not say how big the file is still lets the export run: the
// size only feeds the progress fallback.
func TestAppAPKSurvivesAFailedStat(t *testing.T) {
	f := appAPKFixture(t, func(c Command) (Result, error) {
		switch {
		case containsSubstring(c.Args, "pm list packages"):
			return Result{Output: "package:/data/app/com.foo/base.apk=com.foo\n"}, nil
		case containsSubstring(c.Args, "stat -c"):
			return Result{Output: "stat: not found\n", ExitCode: 1}, nil
		}
		return Result{}, nil
	})
	apk, err := f.m.AppAPK(context.Background(), f.id, "com.foo")
	if err != nil {
		t.Fatalf("AppAPK() error: %v", err)
	}
	if apk.Path == "" || apk.Size != 0 {
		t.Fatalf("AppAPK() = %+v, want the path and an unknown size", apk)
	}
}
