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

//go:embed resources/icon.png
var appIcon []byte

//go:embed resources/tray-light.png
var trayLight []byte

//go:embed resources/tray-dark.png
var trayDark []byte

var running *app.App

func main() {
	// --version answers before anything else: asking what this build is must
	// not compete for the single-instance lock with a running copy.
	demo := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--demo":
			demo = true
		case "--version", "-version", "-v":
			fmt.Println(app.AppName, versionText())
			return
		}
	}
	if !mygo.App.RequestSingleInstanceLock() {
		// Another copy owns the channels; it was asked to show its window.
		return
	}
	mygo.App.OnSecondInstance(func([]string, string) {
		if running != nil {
			running.Show()
		}
	})
	// Every string this app owns is Chinese; this also makes the component
	// library's own labels (toast close, breadcrumb overflow) Chinese.
	mygo.App.SetLocale("zh-Hans")
	mygo.App.WhenReady(func() {
		env := buildEnvironment()
		if demo {
			env = app.NewDemoEnvironment()
		}
		// One icon for the window, the taskbar and the bundle, embedded here so
		// that `go build` and `go run` show it too, not only the packaged app.
		env.Icon = appIcon
		running = app.New(env)
		running.Start(app.TrayIcons{Light: trayLight, Dark: trayDark})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// buildEnvironment loads the configuration and wires the manager.
func buildEnvironment() app.Environment {
	configDir := platform.DefaultConfigDir()
	store := core.NewConfigStore(configDir)

	// Every failure the app can survive is collected here and turned into a
	// page by core.Diagnose. The old code kept the first one only, so fixing it
	// revealed the next one on the following start.
	in := core.DiagnoseInput{ConfigDir: configDir}
	cfg, err := store.Load()
	if err != nil {
		in.ConfigErr = err
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
	in.ADBPath = cfg.Tools.ADB
	in.ScrcpyPath = cfg.Tools.Scrcpy

	credentials := core.CredentialStore(core.NewMemoryCredentialStore())
	if credstore.Available() {
		credentials = credstore.New()
	} else {
		in.CredentialsErr = core.Error("系统的钥匙串 / 凭据管理器不可用")
	}

	// History is optional: if the file cannot be opened the app keeps
	// everything in memory and says so on the startup page.
	// The history file needs its directory to exist before SQLite opens it.
	_ = os.MkdirAll(configDir, 0o700)
	var recorder core.Recorder
	var logs core.HistoryReader
	historyPath := filepath.Join(configDir, "history.db")
	if store, err := history.Open(historyPath); err != nil {
		in.HistoryErr = err
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
		in.AppMetaErr = err
	} else {
		// One store, two narrow interfaces: the resource page reads names, the
		// cache page lists and clears them.
		appMeta = store
		appMetaCache = store
	}
	problems := core.Diagnose(in)
	// The window shows them; stderr keeps them for a terminal launch and CI.
	for _, problem := range problems {
		line := problem.Title
		if problem.Detail != "" {
			line += "：" + problem.Detail
		}
		fmt.Fprintln(os.Stderr, line)
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

	return app.Environment{
		Manager:      manager,
		Locate:       platform.FindTool,
		Runner:       runner,
		ConfigDir:    configDir,
		Problems:     problems,
		OpenDir:      platform.RevealDir,
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
