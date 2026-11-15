package core

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// Local ports handed out when the device does not fix one.
const (
	firstAutoPort = 5556
	lastAutoPort  = 5655
)

// TailcatSession is the part of a tailcat client this app uses.
type TailcatSession interface {
	DialTCPPort(ctx context.Context, port uint16) (net.Conn, error)
	Close() error
}

// TailcatDialer creates a session for a device address. It exists so tests can
// drive the forwarder without a network.
type TailcatDialer interface {
	New(ctx context.Context, address string, nodeKey key.NodePrivate, relay RelayConfig, logf func(format string, args ...any)) (TailcatSession, error)
}

// ForwardRequest describes one device channel.
type ForwardRequest struct {
	Address       string
	PreferredPort int // 0 allocates from 5556 upward
	RemotePort    int
	NodeKey       key.NodePrivate
	Relay         RelayConfig
}

// ForwardConn is an open channel: a local listener whose connections are
// proxied to the device's ADB port over Tailcat.
type ForwardConn struct {
	Port   int
	Serial string
	// Close shuts the listener, the proxied connections, then the session.
	Close func(ctx context.Context) error
}

// Forwarder opens device channels. The zero value is usable.
type Forwarder struct {
	// Dialer is nil in production, which selects the real tailcat client.
	Dialer TailcatDialer
}

// Open binds a local port and starts proxying to the device.
func (f *Forwarder) Open(ctx context.Context, req ForwardRequest, onLog func(string)) (*ForwardConn, error) {
	if !Plausible(req.Address) {
		return nil, Error("请填写以 tc 开头的完整 Tailcat 连接地址")
	}
	if req.RemotePort < 1 || req.RemotePort > 65535 {
		return nil, Error("端口必须在 1–65535 之间；本地端口留空表示自动分配")
	}

	listener, err := f.listen(req.PreferredPort)
	if err != nil {
		return nil, err
	}

	port := listener.Addr().(*net.TCPAddr).Port
	session, err := f.dialer().New(ctx, req.Address, req.NodeKey, req.Relay, logSink(req.Address, onLog))
	if err != nil {
		listener.Close()
		return nil, err
	}

	var (
		once   sync.Once
		active sync.Map // net.Conn -> struct{}
		closed = make(chan struct{})
	)

	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case <-closed:
				local.Close()
				return
			default:
			}
			active.Store(local, struct{}{})
			go func() {
				defer active.Delete(local)
				defer local.Close()
				remote, err := session.DialTCPPort(ctx, uint16(req.RemotePort))
				if err != nil {
					select {
					case <-closed:
					default:
						logLine(req.Address, onLog, fmt.Sprintf("连接设备端口失败：%v", err))
					}
					return
				}
				active.Store(remote, struct{}{})
				defer active.Delete(remote)
				defer remote.Close()
				tailcat.ProxyConns(local, remote)
			}()
		}
	}()

	closeFn := func(context.Context) error {
		once.Do(func() {
			close(closed)
			// The listener goes first so no new connection is accepted while
			// the existing ones are drained.
			_ = listener.Close()
			active.Range(func(key, _ any) bool {
				_ = key.(net.Conn).Close()
				return true
			})
			_ = session.Close()
		})
		return nil
	}

	return &ForwardConn{Port: port, Serial: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Close: closeFn}, nil
}

func (f *Forwarder) dialer() TailcatDialer {
	if f.Dialer != nil {
		return f.Dialer
	}
	return libDialer{}
}

func (f *Forwarder) listen(preferred int) (net.Listener, error) {
	if preferred > 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(preferred)))
		if err != nil {
			return nil, Errorf("本地端口 %d 已由另一会话使用或正在释放", preferred)
		}
		return ln, nil
	}
	for port := firstAutoPort; port <= lastAutoPort; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return ln, nil
		}
	}
	return nil, Error("5556–5655 均被占用，请为设备指定其他本地端口")
}

// logSink turns a tailcat log callback into redacted UI lines.
func logSink(address string, onLog func(string)) func(format string, args ...any) {
	return func(format string, args ...any) {
		if onLog == nil {
			return
		}
		logLine(address, onLog, fmt.Sprintf(format, args...))
	}
}

func logLine(address string, onLog func(string), msg string) {
	if onLog == nil {
		return
	}
	text := strings.TrimRight(Redact(msg, address), "\n")
	if text == "" {
		return
	}
	onLog(text + "\n")
}

// libDialer is the production dialer: one tailcat client per device channel.
type libDialer struct{}

func (libDialer) New(ctx context.Context, address string, nodeKey key.NodePrivate, relay RelayConfig, logf func(string, ...any)) (TailcatSession, error) {
	server := tailcat.Addr(address)
	if relay.Kind == RelayInline {
		resolved, err := server.Resolve(ctx, relay.Map)
		if err != nil {
			return nil, Errorf("无法解析 DERP 配置：%v", err)
		}
		server = resolved
	}
	client := &tailcat.Client{Server: server, Key: nodeKey}
	if relay.Kind == RelayURL {
		client.DERPMapURL = relay.URL
	}
	if logf != nil {
		client.Logf = logf
	}
	return &libSession{client: client}, nil
}

type libSession struct{ client *tailcat.Client }

func (s *libSession) DialTCPPort(ctx context.Context, port uint16) (net.Conn, error) {
	return s.client.DialTCPPort(ctx, port)
}

func (s *libSession) Close() error { return s.client.Close() }
