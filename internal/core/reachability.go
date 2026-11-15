package core

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ReachabilityState is where one device is in a batch reachability test.
type ReachabilityState string

// The reachability states.
const (
	ReachQueued      ReachabilityState = "queued"
	ReachTesting     ReachabilityState = "testing"
	ReachReachable   ReachabilityState = "reachable"
	ReachUnreachable ReachabilityState = "unreachable"
	ReachFailed      ReachabilityState = "failed"
	ReachCancelled   ReachabilityState = "cancelled"
)

// Label is the text the table shows.
func (s ReachabilityState) Label() string {
	switch s {
	case ReachQueued:
		return "等待测试"
	case ReachTesting:
		return "正在测试"
	case ReachReachable:
		return "可达"
	case ReachUnreachable:
		return "未收到 pong"
	case ReachFailed:
		return "测试失败"
	case ReachCancelled:
		return "已取消"
	default:
		return string(s)
	}
}

// ReachabilityResult is one row of the batch test table.
type ReachabilityResult struct {
	ID        uuid.UUID
	Name      string
	Temporary bool
	State     ReachabilityState
	Latency   string
	Route     string
	Detail    string
	CheckedAt time.Time
	ADBState  ConnectionState
}

// Prober runs one link probe. Pinger implements it.
type Prober interface {
	Ping(ctx context.Context, deviceID uuid.UUID, address string, seconds int, relay RelayConfig) (PingOutcome, error)
}

// Checker runs a batch of probes, three at a time.
type Checker struct {
	prober   Prober
	recorder Recorder

	mu      sync.Mutex
	results []ReachabilityResult
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewChecker returns a checker that uses prober.
func NewChecker(prober Prober) *Checker {
	return &Checker{prober: prober}
}

// WithRecorder persists each finished batch.
func (c *Checker) WithRecorder(recorder Recorder) *Checker {
	c.recorder = recorder
	return c
}

func (c *Checker) records() []ReachabilityRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ReachabilityRecord, 0, len(c.results))
	for _, r := range c.results {
		out = append(out, ReachabilityRecord{
			DeviceID: r.ID, Name: r.Name, State: string(r.State),
			Latency: r.Latency, Route: r.Route, Detail: r.Detail, CheckedAt: r.CheckedAt,
		})
	}
	return out
}

// Results returns a copy of the current table.
func (c *Checker) Results() []ReachabilityResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ReachabilityResult(nil), c.results...)
}

// Running reports whether a batch is in flight.
func (c *Checker) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Start begins a batch; it is a no-op while another batch runs.
func (c *Checker) Start(profiles []Profile, temporaryIDs map[uuid.UUID]bool,
	adbState func(uuid.UUID) ConnectionState, address func(uuid.UUID) (string, error)) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	rows := make([]ReachabilityResult, len(profiles))
	for i, p := range profiles {
		rows[i] = ReachabilityResult{
			ID:        p.ID,
			Name:      p.Name,
			Temporary: temporaryIDs[p.ID],
			State:     ReachQueued,
			Latency:   PlaceholderUnknown,
			Route:     PlaceholderUnknown,
			ADBState:  adbState(p.ID),
		}
	}
	c.results = rows
	c.running = true
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	done := make(chan struct{})
	c.done = done
	c.mu.Unlock()

	if len(profiles) == 0 {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		close(done)
		return
	}

	go func() {
		defer func() {
			c.mu.Lock()
			for i := range c.results {
				if c.results[i].State == ReachQueued || c.results[i].State == ReachTesting {
					c.results[i].State = ReachCancelled
				}
			}
			c.running = false
			c.mu.Unlock()
			if c.recorder != nil {
				c.recorder.RecordReachability(c.records())
			}
			close(done)
		}()

		const workers = 3
		indexes := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range indexes {
					c.check(ctx, i, profiles[i], adbState, address)
				}
			}()
		}
		for i := range profiles {
			select {
			case <-ctx.Done():
			case indexes <- i:
				continue
			}
			break
		}
		close(indexes)
		wg.Wait()
	}()
}

// Cancel stops a running batch; unfinished rows end as cancelled.
func (c *Checker) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Wait blocks until the current batch finished.
func (c *Checker) Wait() {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (c *Checker) setRow(index int, mutate func(*ReachabilityResult)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index < len(c.results) {
		mutate(&c.results[index])
	}
}

func (c *Checker) check(ctx context.Context, index int, profile Profile,
	adbState func(uuid.UUID) ConnectionState, address func(uuid.UUID) (string, error)) {
	c.setRow(index, func(r *ReachabilityResult) { r.State = ReachTesting })
	defer c.setRow(index, func(r *ReachabilityResult) {
		r.CheckedAt = time.Now()
		r.ADBState = adbState(profile.ID)
	})

	credential, err := address(profile.ID)
	if err != nil {
		c.setRow(index, func(r *ReachabilityResult) {
			r.State = ReachFailed
			r.Detail = truncateDetail(sanitizeError(err).Error())
		})
		return
	}
	if !Plausible(credential) {
		c.setRow(index, func(r *ReachabilityResult) {
			r.State = ReachFailed
			r.Detail = truncateDetail("没有有效连接凭据，请编辑设备后重试")
		})
		return
	}
	relay, err := NormalizeRelay(profile.RelaySource)
	if err != nil {
		c.setRow(index, func(r *ReachabilityResult) {
			r.State = ReachFailed
			r.Detail = truncateDetail(err.Error())
		})
		return
	}
	outcome, err := c.prober.Ping(ctx, profile.ID, credential, 5, relay)
	if err != nil {
		state := ReachFailed
		if err == ErrProbeTimeout {
			state = ReachUnreachable
		}
		if err == ErrCancelled || ctx.Err() != nil {
			state = ReachCancelled
		}
		c.setRow(index, func(r *ReachabilityResult) {
			r.State = state
			r.Detail = truncateDetail(sanitizeError(err).Error())
		})
		return
	}
	c.setRow(index, func(r *ReachabilityResult) {
		r.State = ReachReachable
		r.Latency = formatLatency(outcome.Latency)
		r.Route = outcome.Route
	})
}

func truncateDetail(text string) string {
	runes := []rune(text)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return text
}

func formatLatency(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	return strconv.FormatFloat(ms, 'f', -1, 64) + "ms"
}

// TestAllDevices starts a batch probe over every device.
func (m *Manager) TestAllDevices() {
	profiles := m.AllProfiles()
	temporary := map[uuid.UUID]bool{}
	m.mu.Lock()
	for _, p := range m.temporary {
		temporary[p.ID] = true
	}
	m.mu.Unlock()
	m.checker.Start(profiles, temporary,
		func(id uuid.UUID) ConnectionState { return m.Session(id).State },
		func(id uuid.UUID) (string, error) { return m.AddressForEditing(id) })
}

// Reachability returns the batch checker the window shows.
func (m *Manager) Reachability() *Checker { return m.checker }

// SortedResults returns the table ordered by name for stable rendering.
func (c *Checker) SortedResults() []ReachabilityResult {
	out := c.Results()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
