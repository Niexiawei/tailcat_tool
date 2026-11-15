package app

import (
	"context"
	"os"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

// demoRunner answers adb calls the way a healthy device would.
type demoRunner struct{}

func (demoRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("demo mode runs no processes")
}

func (demoRunner) Run(_ context.Context, c core.Command, onOutput func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg == "get-state" {
			return core.Result{Output: "device\n"}, nil
		}
	}
	time.Sleep(50 * time.Millisecond)
	if onOutput != nil {
		onOutput("示例任务完成，未执行真实设备命令。\n")
	}
	return core.Result{Output: "示例任务完成，未执行真实设备命令。\n"}, nil
}

// demoOpener hands out a fake channel without touching the network.
type demoOpener struct{ next int }

func (o *demoOpener) Open(_ context.Context, req core.ForwardRequest, onLog func(string)) (*core.ForwardConn, error) {
	port := req.PreferredPort
	if port == 0 {
		port = 5556 + o.next
		o.next++
	}
	if onLog != nil {
		onLog("示例通道监听 127.0.0.1:" + itoa(port) + "\n")
	}
	return &core.ForwardConn{
		Port:   port,
		Serial: "127.0.0.1:" + itoa(port),
		Close:  func(context.Context) error { return nil },
	}, nil
}

// NewDemoEnvironment builds the simulated app used by --demo.
func NewDemoEnvironment() Environment {
	profiles := []core.Profile{
		{ID: uuid.New(), Name: "E245 · 示例", RemotePort: 5555, PreferredPort: portPtr(5556)},
		{ID: uuid.New(), Name: "EX5 · 示例", RemotePort: 5555, PreferredPort: portPtr(5557)},
	}
	creds := core.NewMemoryCredentialStore()
	for _, profile := range profiles {
		_ = creds.Write("tc"+repeatChar('A', 30), profile.ID)
	}
	// The demo never runs a process, so pointing ADB at this executable keeps
	// the tool row honest (present) without spawning anything.
	self, _ := os.Executable()
	manager := core.NewManager(core.ManagerOptions{
		Configuration: core.Configuration{
			Profiles: profiles,
			Tools:    core.ToolSettings{ADB: self, Scrcpy: "", AddressBookDir: "/demo/tailcat-adb"},
		},
		Credentials: creds,
		Identities:  core.NewIdentityStore(demoIdentityDir()),
		Runner:      demoRunner{},
		Forwarder:   &demoOpener{},
		Locate:      func(string) (string, bool) { return "", false },
	})
	return Environment{Manager: manager, Locate: func(string) (string, bool) { return "", false }, Demo: true}
}

func portPtr(v int) *int { return &v }

// demoIdentityDir keeps the simulated identities in the OS temp directory so
// demo mode never touches the real identity files.
func demoIdentityDir() string {
	dir, err := os.MkdirTemp("", "tailcat-demo-identities")
	if err != nil {
		return os.TempDir()
	}
	return dir
}

func repeatChar(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}
