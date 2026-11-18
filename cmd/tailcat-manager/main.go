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
	"github.com/Niexiawei/tailcat-tool/internal/appmeta"
	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/Niexiawei/tailcat-tool/internal/credstore"
	"github.com/Niexiawei/tailcat-tool/internal/history"
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
	// Every string this app owns is Chinese; this also makes the component
	// library's own labels (toast close, breadcrumb overflow) Chinese.
	mygo.App.SetLocale("zh-Hans")
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

	// History is optional: if the file cannot be opened the app keeps
	// everything in memory and says so once.
	// The history file needs its directory to exist before SQLite opens it.
	_ = os.MkdirAll(configDir, 0o700)
	var recorder core.Recorder
	var logs core.HistoryReader
	historyPath := filepath.Join(configDir, "history.db")
	if store, err := history.Open(historyPath); err != nil {
		if notice == "" {
			notice = "历史库无法打开，本次运行的历史不会保存：" + err.Error()
		}
	} else {
		recorder = store
		logs = store
	}

	runner := core.NewExecRunner()
	// Application names and icons come from the device's own APKs, cached for a
	// week under the configuration directory. A cache that will not open costs
	// the names, not the app.
	var appMeta core.AppMetaSource
	var appMetaCache core.AppMetaCache
	if store, err := appmeta.New(filepath.Join(configDir, "appmeta"), runner, 2); err != nil {
		if notice == "" {
			notice = "应用信息缓存不可用，资源页只显示包名：" + err.Error()
		}
	} else {
		// One store, two narrow interfaces: the resource page reads names, the
		// cache page lists and clears them.
		appMeta = store
		appMetaCache = store
	}
	manager := core.NewManager(core.ManagerOptions{
		Config:        store,
		Configuration: cfg,
		Credentials:   core.NewCachedCredentialStore(credentials),
		Identities:    core.NewIdentityStore(filepath.Join(configDir, "Identities")),
		Runner:        runner,
		Locate:        platform.FindTool,
		Recorder:      recorder,
		AppMeta:       appMeta,
		AppMetaCache:  appMetaCache,
	})
	if recorder != nil {
		if recent, err := recorder.RecentTasks(50); err == nil {
			manager.LoadHistory(recent)
		}
	}
	if notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}

	return app.Environment{
		Manager:      manager,
		Locate:       platform.FindTool,
		Runner:       runner,
		ConfigDir:    configDir,
		Notice:       notice,
		Install:      newInstaller(runner),
		History:      logs,
		RecordAppLog: manager.RecordAppLog,
		OnShutdown:   func() { manager.Shutdown(context.Background()) },
	}
}

// newInstaller returns the component installer wired to this platform.
func newInstaller(runner core.Runner) *core.Installer {
	return core.NewInstaller(runner, platform.FindTool, platform.PackageManager)
}
