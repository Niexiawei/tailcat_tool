package core

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tailscale/tailcat"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// PingOutcome is what one link probe learned.
type PingOutcome struct {
	Latency time.Duration
	Direct  bool
	Route   string
}

// PingResult is what a session reports back; Route is already user-facing.
type PingResult struct {
	Latency time.Duration
	Direct  bool
	Route   string
}

// PingSession is the part of a tailcat client a probe needs.
type PingSession interface {
	DiscoPing(ctx context.Context) (PingResult, error)
	Close() error
}

// PingDialer creates a probe session. Tests inject a fake here.
type PingDialer interface {
	New(ctx context.Context, address string, nodeKey key.NodePrivate, relay RelayConfig, logf func(string, ...any)) (PingSession, error)
}

// IdentityProvider hands out the saved device identities.
type IdentityProvider interface {
	Identity(ctx context.Context, name string) (Identity, error)
	NodeKey(ctx context.Context, name string) (key.NodePrivate, error)
}

// Pinger runs link probes. Probes for the same device are serialized because
// they share one saved probe identity, and two live tailcat clients with the
// same identity fight over the same DERP channel.
type Pinger struct {
	Identities IdentityProvider
	Dialer     PingDialer

	lanes sync.Map // uuid.UUID -> *sync.Mutex
}

func (p *Pinger) lane(deviceID uuid.UUID) *sync.Mutex {
	if v, ok := p.lanes.Load(deviceID); ok {
		return v.(*sync.Mutex)
	}
	actual, _ := p.lanes.LoadOrStore(deviceID, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// Ping probes one device with its dedicated probe identity.
func (p *Pinger) Ping(ctx context.Context, deviceID uuid.UUID, address string, seconds int, relay RelayConfig) (PingOutcome, error) {
	if !Plausible(address) {
		return PingOutcome{}, Error("没有有效连接凭据，请编辑设备或导入地址簿")
	}
	mu := p.lane(deviceID)
	mu.Lock()
	defer mu.Unlock()

	if err := ctx.Err(); err != nil {
		return PingOutcome{}, ErrCancelled
	}
	name := deviceID.String() + "-probe"
	if _, err := p.Identities.Identity(ctx, name); err != nil {
		return PingOutcome{}, err
	}
	nodeKey, err := p.Identities.NodeKey(ctx, name)
	if err != nil {
		return PingOutcome{}, err
	}
	dialer := p.Dialer
	if dialer == nil {
		dialer = libPingDialer{}
	}
	session, err := dialer.New(ctx, address, nodeKey, relay, nil)
	if err != nil {
		return PingOutcome{}, err
	}
	defer session.Close()

	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	res, err := session.DiscoPing(probeCtx)
	if err != nil {
		if ctx.Err() != nil {
			return PingOutcome{}, ErrCancelled
		}
		return PingOutcome{}, Errorf("链路探测失败：%v", err)
	}
	return PingOutcome{Latency: res.Latency, Direct: res.Direct, Route: res.Route}, nil
}

// relayRoute turns a ping endpoint into the wording the UI shows.
func relayRoute(endpoint, regionCode string, regionID int) (direct bool, route string) {
	if endpoint != "" {
		return true, "直连 · " + endpoint
	}
	if regionCode != "" {
		return false, "中继 · DERP(" + regionCode + ")"
	}
	return false, "中继 · DERP(" + strconv.Itoa(regionID) + ")"
}

// libPingDialer is the production probe dialer: one short-lived tailcat
// client per probe, created with the device's saved probe identity.
type libPingDialer struct{}

func (libPingDialer) New(ctx context.Context, address string, nodeKey key.NodePrivate, relay RelayConfig, logf func(string, ...any)) (PingSession, error) {
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
	return &libPingSession{client: client}, nil
}

type libPingSession struct{ client *tailcat.Client }

func (s *libPingSession) DiscoPing(ctx context.Context) (PingResult, error) {
	res, err := s.client.DiscoPing(ctx)
	if err != nil {
		return PingResult{}, err
	}
	return pingResultFrom(res), nil
}

func (s *libPingSession) Close() error { return s.client.Close() }

func pingResultFrom(res *ipnstate.PingResult) PingResult {
	latency := time.Duration(res.LatencySeconds * float64(time.Second)).Round(10 * time.Microsecond)
	direct, route := relayRoute(res.Endpoint, res.DERPRegionCode, int(res.DERPRegionID))
	return PingResult{Latency: latency, Direct: direct, Route: route}
}
