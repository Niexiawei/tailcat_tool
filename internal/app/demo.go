package app

import (
	"context"
	"os"
	"strings"
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
	joined := strings.Join(c.Args, " ")
	switch {
	case strings.Contains(joined, "@@STAT"):
		return core.Result{Output: demoResourceOutput}, nil
	case strings.Contains(joined, "pm path"):
		return core.Result{Output: "package:/data/app/com.demo.camera/base.apk\n"}, nil
	case strings.Contains(joined, "stat -c"):
		return core.Result{Output: "4096 1700000000\n"}, nil
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
			Tools:    core.ToolSettings{ADB: self},
		},
		Credentials: creds,
		Identities:  core.NewIdentityStore(demoIdentityDir()),
		Runner:      demoRunner{},
		Forwarder:   &demoOpener{},
		Locate:      func(string) (string, bool) { return "", false },
		AppMeta:     demoAppMeta{},
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

// demoAppMeta gives the simulated devices names and icons without any adb: the
// real one pulls an APK, which --demo never does.
type demoAppMeta struct{}

// demoIcon is a 1×1 PNG; Avatar cover-crops it into a circle.
var demoIcon = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func (demoAppMeta) Lookup(_, pkg string) (core.AppMeta, bool) {
	switch pkg {
	case "com.demo.camera":
		return core.AppMeta{Package: pkg, Label: "相机", Icon: demoIcon}, true
	case "com.demo.gallery":
		return core.AppMeta{Package: pkg, Label: "相册", Icon: demoIcon}, true
	}
	return core.AppMeta{}, false
}

// Request and Refresh have nothing to do: the demo has no APK to read.
func (demoAppMeta) Request(string, string, string, []string) {}

func (demoAppMeta) Refresh(string, string, string, []string) {}

// demoResourceOutput is what the simulated device prints for one sampling
// round: two applications, a system process and no GPU counter.
const demoResourceOutput = `@@STAT
cpu  12000 0 3000 90000 500 0 300 0 0 0
@@MEM
MemTotal:        8000000 kB
MemAvailable:    2500000 kB
@@CPU
8
Hardware	: Qualcomm Technologies, Inc SM8150
@@GPU
@@PROC
@@P 100
100 (cameraserver) S 1 100 100 0 -1 0 0 0 0 0 900 200 0 0 20 0 12 0 5000 0 0
VmRSS:	   204800 kB
com.demo.camera 
@@P 101
101 (camera:remote) S 1 101 101 0 -1 0 0 0 0 0 120 40 0 0 20 0 4 0 5100 0 0
VmRSS:	    30720 kB
com.demo.camera:remote 
@@P 200
200 (gallery) S 1 200 200 0 -1 0 0 0 0 0 300 60 0 0 20 0 8 0 5200 0 0
VmRSS:	    81920 kB
com.demo.gallery 
@@P 300
300 (system_server) S 1 300 300 0 -1 0 0 0 0 0 800 700 0 0 20 0 40 0 4000 0 0
VmRSS:	   102400 kB
system_server 
`
