package core

import (
	"context"
	"strings"
	"time"
)

// installTimeout bounds one package-manager run.
const installTimeout = 30 * time.Minute

// brewEnv turns off everything Homebrew would do besides the install itself.
var brewEnv = []string{
	"HOMEBREW_NO_AUTO_UPDATE=1",
	"HOMEBREW_NO_INSTALL_UPGRADE=1",
	"HOMEBREW_NO_INSTALL_CLEANUP=1",
	"HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1",
	"HOMEBREW_NO_ENV_HINTS=1",
	"HOMEBREW_NO_ASK=1",
	"NONINTERACTIVE=1",
}

// InstallArgs maps a component to the package manager's argv. The boolean is
// false when that manager has no package for the component, so the UI can say
// "install it yourself" instead of guessing.
func InstallArgs(comp ToolComponent, pm string) ([]string, bool) {
	switch pm {
	case "brew":
		if comp == ToolADB {
			return []string{"install", "--cask", "android-platform-tools"}, true
		}
		if comp == ToolScrcpy {
			return []string{"install", "scrcpy"}, true
		}
	case "winget":
		if comp == ToolADB {
			return []string{"install", "--id", "Google.PlatformTools",
				"--accept-source-agreements", "--accept-package-agreements"}, true
		}
		// winget has no dependable scrcpy package.
		return nil, false
	case "apt":
		if comp == ToolADB {
			return []string{"install", "-y", "android-tools-adb"}, true
		}
		if comp == ToolScrcpy {
			return []string{"install", "-y", "scrcpy"}, true
		}
	case "dnf":
		if comp == ToolADB {
			return []string{"install", "-y", "android-tools"}, true
		}
		if comp == ToolScrcpy {
			return []string{"install", "-y", "scrcpy"}, true
		}
	case "pacman":
		if comp == ToolADB {
			return []string{"-S", "--noconfirm", "android-tools"}, true
		}
		if comp == ToolScrcpy {
			return []string{"-S", "--noconfirm", "scrcpy"}, true
		}
	}
	return nil, false
}

// Installer installs missing tools through the host package manager.
type Installer struct {
	Runner         Runner
	Locate         LocateTool
	PackageManager func() (name, path string, ok bool)
}

// NewInstaller returns an installer wired to the real system.
func NewInstaller(runner Runner, locate LocateTool, pm func() (string, string, bool)) *Installer {
	return &Installer{Runner: runner, Locate: locate, PackageManager: pm}
}

// HomebrewPath returns the brew path when Homebrew is present.
func (in *Installer) HomebrewPath() (string, bool) {
	pm, path, ok := in.pm()
	if !ok || pm != "brew" {
		return "", false
	}
	return path, true
}

func (in *Installer) pm() (string, string, bool) {
	if in.PackageManager == nil {
		return "", "", false
	}
	return in.PackageManager()
}

// Install installs the components in order. A component that is already on the
// system is configured straight away instead of being installed again.
func (in *Installer) Install(ctx context.Context, comps []ToolComponent, onOutput func(string), configure func(ToolComponent, string) error) error {
	if onOutput == nil {
		onOutput = func(string) {}
	}
	if configure == nil {
		configure = func(ToolComponent, string) error { return nil }
	}
	for _, comp := range comps {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}
		if path, ok := in.locate(comp.ExecutableName()); ok {
			if err := configure(comp, path); err != nil {
				return err
			}
			onOutput(comp.Label() + " 已检测并配置\n")
			continue
		}
		pm, pmPath, ok := in.pm()
		if !ok {
			return Error("未检测到 Homebrew，请先从 brew.sh 安装 Homebrew，然后重试；也可以手动安装后在设置中指定路径")
		}
		args, available := InstallArgs(comp, pm)
		if !available {
			return Errorf("当前包管理器（%s）没有 %s 的安装包，请手动安装后在设置中指定可执行文件路径", pm, comp.Label())
		}
		redactor := NewRedactor("")
		res, err := in.runner().Run(ctx, Command{
			Path:    pmPath,
			Args:    args,
			Env:     InstallEnv(pm),
			Timeout: installTimeout,
		}, func(chunk string) {
			if safe := redactor.Append(chunk); safe != "" {
				onOutput(safe)
			}
		})
		if tail := redactor.Flush(); tail != "" {
			onOutput(tail)
		}
		if err != nil {
			return sanitizeError(err)
		}
		if res.Cancelled || ctx.Err() != nil {
			return ErrCancelled
		}
		if reqErr := res.RequireSuccess(); reqErr != nil {
			return reqErr
		}
		path, ok := in.locate(comp.ExecutableName())
		if !ok {
			return Errorf("包管理器已完成，但未找到 %s 可执行文件，请检查安装日志和链接状态", comp.Label())
		}
		if err := configure(comp, path); err != nil {
			return err
		}
	}
	return nil
}

// InstallMissing installs whatever is not on the system yet and configures the
// rest. Install already skips the package manager for tools it finds, so this
// is the same walk.
func (in *Installer) InstallMissing(ctx context.Context, comps []ToolComponent, onOutput func(string), configure func(ToolComponent, string) error) error {
	return in.Install(ctx, comps, onOutput, configure)
}

func (in *Installer) runner() Runner {
	if in.Runner == nil {
		return NewExecRunner()
	}
	return in.Runner
}

func (in *Installer) locate(name string) (string, bool) {
	if in.Locate == nil {
		return "", false
	}
	return in.Locate(name)
}

// InstallEnv is the environment a package-manager run gets: nothing may
// upgrade or clean anything else along the way.
func InstallEnv(pm string) []string {
	if pm == "brew" {
		return append([]string(nil), brewEnv...)
	}
	return []string{"NONINTERACTIVE=1", "DEBIAN_FRONTEND=noninteractive"}
}

// InstallArgsText renders the command for the button's tooltip.
func InstallArgsText(comp ToolComponent, pm string) string {
	args, ok := InstallArgs(comp, pm)
	if !ok {
		return ""
	}
	return strings.TrimSpace(pm + " " + strings.Join(args, " "))
}
