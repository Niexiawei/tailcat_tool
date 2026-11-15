// Command tailcat-manager is the desktop GUI of Tailcat Manager.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Niexiawei/tailcat-tool/internal/app"
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/Niexiawei/tailcat-tool/internal/credstore"
	"github.com/Niexiawei/tailcat-tool/internal/platform"
	"github.com/egoist/mygo"
)

//go:embed resources/tray.png
var trayIcon []byte

var running *app.App

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		// Another copy owns the channels; it was asked to show its window.
		return
	}
	mygo.App.OnSecondInstance(func([]string, string) {
		if running != nil {
			running.Show()
		}
	})
	demo := false
	for _, arg := range os.Args[1:] {
		if arg == "--demo" {
			demo = true
		}
	}
	mygo.App.WhenReady(func() {
		env := buildEnvironment()
		if demo {
			env = app.NewDemoEnvironment()
		}
		running = app.New(env)
		running.Start(trayIcon)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// buildEnvironment loads the configuration and wires the manager.
func buildEnvironment() app.Environment {
	configDir := platform.DefaultConfigDir()
	store := core.NewConfigStore(configDir)

	var notice string
	cfg, err := store.Load()
	if err != nil {
		notice = "配置读取失败，已使用默认设置：" + err.Error()
		cfg = core.Configuration{}
	}
	if cfg.Tools.AddressBookDir == "" {
		cfg.Tools.AddressBookDir = platform.DefaultAddressBookDir()
	}
	if cfg.Tools.ADB == "" {
		if path, ok := platform.FindTool("adb"); ok {
			cfg.Tools.ADB = path
		}
	}
	if cfg.Tools.Scrcpy == "" {
		if path, ok := platform.FindTool("scrcpy"); ok {
			cfg.Tools.Scrcpy = path
		}
	}

	credentials := core.CredentialStore(core.NewMemoryCredentialStore())
	if credstore.Available() {
		credentials = credstore.New()
	} else if notice == "" {
		notice = "未检测到系统凭据库，连接地址仅在本次运行中保留"
	}

	runner := core.NewExecRunner()
	manager := core.NewManager(core.ManagerOptions{
		Config:        store,
		Configuration: cfg,
		Credentials:   core.NewCachedCredentialStore(credentials),
		Identities:    core.NewIdentityStore(filepath.Join(configDir, "Identities")),
		Runner:        runner,
		Locate:        platform.FindTool,
	})
	if notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}

	return app.Environment{
		Manager:    manager,
		Locate:     platform.FindTool,
		Runner:     runner,
		ConfigDir:  configDir,
		Notice:     notice,
		Install:    newInstaller(runner),
		OnShutdown: func() { manager.Shutdown(context.Background()) },
	}
}

// newInstaller returns the component installer wired to this platform.
func newInstaller(runner core.Runner) *core.Installer {
	return core.NewInstaller(runner, platform.FindTool, platform.PackageManager)
}
