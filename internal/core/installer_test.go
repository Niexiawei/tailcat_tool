package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newInstallerForTest(t *testing.T, pm string, pmPath string, located map[string]string) (*Installer, *fakeRunner) {
	t.Helper()
	runner := &fakeRunner{}
	in := &Installer{
		Runner: runner,
		Locate: func(name string) (string, bool) {
			if path, ok := located[name]; ok {
				return path, true
			}
			return "", false
		},
		PackageManager: func() (string, string, bool) {
			if pm == "" {
				return "", "", false
			}
			return pm, pmPath, true
		},
	}
	return in, runner
}

func TestInstallArgs(t *testing.T) {
	cases := []struct {
		pm   string
		comp ToolComponent
		want []string
		ok   bool
	}{
		{"brew", ToolADB, []string{"install", "--cask", "android-platform-tools"}, true},
		{"brew", ToolScrcpy, []string{"install", "scrcpy"}, true},
		{"winget", ToolADB, []string{"install", "--id", "Google.PlatformTools", "--accept-source-agreements", "--accept-package-agreements"}, true},
		{"winget", ToolScrcpy, nil, false},
		{"apt", ToolADB, []string{"install", "-y", "android-tools-adb"}, true},
		{"apt", ToolScrcpy, []string{"install", "-y", "scrcpy"}, true},
		{"dnf", ToolADB, []string{"install", "-y", "android-tools"}, true},
		{"pacman", ToolADB, []string{"-S", "--noconfirm", "android-tools"}, true},
		{"unknown", ToolADB, nil, false},
	}
	for _, tc := range cases {
		got, ok := InstallArgs(tc.comp, tc.pm)
		if ok != tc.ok || strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Fatalf("InstallArgs(%s, %s) = (%v, %v), want (%v, %v)", tc.comp, tc.pm, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInstallerEnvSuppression(t *testing.T) {
	in, runner := newInstallerForTest(t, "brew", "/opt/homebrew/bin/brew", map[string]string{})
	// After the install the tool must appear for the post-check.
	runner.setHandler(func(_ context.Context, c Command) (Result, error) { return Result{}, nil })
	if err := in.Install(context.Background(), []ToolComponent{ToolADB}, nil, func(ToolComponent, string) error { return nil }); err == nil {
		t.Fatal("install without the tool appearing afterwards should fail")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 1 {
		t.Fatalf("runner saw %d calls, want 1", len(runner.calls))
	}
	env := strings.Join(runner.calls[0].Env, " ")
	for _, want := range []string{
		"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_UPGRADE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1",
		"HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_ASK=1", "NONINTERACTIVE=1",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env %v lacks %q", runner.calls[0].Env, want)
		}
	}
	if runner.calls[0].Timeout != 1800*time.Second {
		t.Fatalf("timeout = %v, want 30min", runner.calls[0].Timeout)
	}
}

func TestInstallerSkipsExisting(t *testing.T) {
	in, runner := newInstallerForTest(t, "brew", "/opt/homebrew/bin/brew", map[string]string{"adb": "/usr/bin/adb"})
	var configured []string
	err := in.Install(context.Background(), []ToolComponent{ToolADB}, nil, func(comp ToolComponent, path string) error {
		configured = append(configured, string(comp)+"="+path)
		return nil
	})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("installer ran %d commands although adb was found", len(runner.calls))
	}
	if len(configured) != 1 || configured[0] != "adb=/usr/bin/adb" {
		t.Fatalf("configured = %v", configured)
	}
}

func TestInstallerMissingPackageManager(t *testing.T) {
	in, _ := newInstallerForTest(t, "", "", map[string]string{})
	err := in.Install(context.Background(), []ToolComponent{ToolADB}, nil, func(ToolComponent, string) error { return nil })
	if err == nil {
		t.Fatal("Install() succeeded without a package manager")
	}
	if !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("error = %v, want the Homebrew hint", err)
	}
}

func TestInstallerCannotAutoInstall(t *testing.T) {
	in, runner := newInstallerForTest(t, "winget", "winget.exe", map[string]string{})
	err := in.Install(context.Background(), []ToolComponent{ToolScrcpy}, nil, func(ToolComponent, string) error { return nil })
	if err == nil {
		t.Fatal("Install() claimed it could install scrcpy through winget")
	}
	if len(runner.calls) != 0 {
		t.Fatal("installer ran a command with no package for the component")
	}
}

func TestInstallerCancel(t *testing.T) {
	in, runner := newInstallerForTest(t, "brew", "/opt/homebrew/bin/brew", map[string]string{})
	runner.setHandler(func(ctx context.Context, c Command) (Result, error) {
		<-ctx.Done()
		return Result{Cancelled: true}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := in.Install(ctx, []ToolComponent{ToolADB}, nil, func(ToolComponent, string) error { return nil })
	if err == nil {
		t.Fatal("Install() ignored cancellation")
	}
}

func TestInstallMissingComponents(t *testing.T) {
	t.Run("both already present", func(t *testing.T) {
		in, runner := newInstallerForTest(t, "brew", "/opt/homebrew/bin/brew",
			map[string]string{"adb": "/usr/bin/adb", "scrcpy": "/usr/bin/scrcpy"})
		var configured []ToolComponent
		if err := in.InstallMissing(context.Background(), []ToolComponent{ToolADB, ToolScrcpy}, nil,
			func(comp ToolComponent, path string) error {
				configured = append(configured, comp)
				return nil
			}); err != nil {
			t.Fatalf("InstallMissing() error: %v", err)
		}
		if len(configured) != 2 {
			t.Fatalf("configured = %v, want both", configured)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("installer ran %d commands although both tools exist", len(runner.calls))
		}
	})

	t.Run("a failing install stops the batch and names the tool", func(t *testing.T) {
		in, runner := newInstallerForTest(t, "brew", "/opt/homebrew/bin/brew",
			map[string]string{"scrcpy": "/usr/bin/scrcpy"})
		runner.setHandler(func(_ context.Context, c Command) (Result, error) { return Result{}, nil })
		var configured []ToolComponent
		err := in.InstallMissing(context.Background(), []ToolComponent{ToolADB, ToolScrcpy}, nil,
			func(comp ToolComponent, path string) error {
				configured = append(configured, comp)
				return nil
			})
		if err == nil {
			t.Fatal("InstallMissing() reported success although adb never appeared")
		}
		if !strings.Contains(err.Error(), "ADB") {
			t.Fatalf("error = %v, want it to name ADB", err)
		}
		if len(configured) != 0 {
			t.Fatalf("configured = %v, want nothing after the failure", configured)
		}
	})
}
