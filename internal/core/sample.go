package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ResourceSnapshot is one reading of a device's resource use.
type ResourceSnapshot struct {
	At     time.Time
	CPU    CPUSample
	Memory MemorySample
	GPU    GPUSample
	Apps   []AppUsage
	// Warnings are the parts this reading could not fill in, in Chinese, for
	// the page to show under the charts.
	Warnings []string
}

// CPUSample is the device's overall CPU use.
type CPUSample struct {
	Model string
	Cores int
	// Percent is 0–100 of all cores together.
	Percent float64
	// Available is false until a second reading gives a delta; the page shows
	// "—" rather than a made-up 0%.
	Available bool
}

// MemorySample is the device's RAM use. OK is false when /proc/meminfo could
// not be read; Available is the free-and-reclaimable byte count.
type MemorySample struct {
	Total, Available, Used uint64
	Percent                float64
	OK                     bool
}

// GPUSample is the device's GPU use. Vendors expose it through their own sysfs
// nodes, so a device without one is unavailable, never 0%.
type GPUSample struct {
	Renderer            string
	Percent             float64
	Available           bool
	PerProcessAvailable bool
}

// resourceCounters is what the next round subtracts from.
type resourceCounters struct {
	cpu   cpuTimes
	ok    bool
	procs map[int]procCounters
}

type procCounters struct {
	jiffies uint64
	start   uint64
}

type hardwareInfo struct {
	renderer string
	fetched  bool
}

// WarningNoGPU explains a device whose vendor exposes no GPU counter.
const WarningNoGPU = "该设备不提供 GPU 使用率"

const (
	// defaultResourceInterval is how often the open resource page re-reads.
	defaultResourceInterval = 2 * time.Second
	resourceSampleTimeout   = 8 * time.Second
	hardwareTimeout         = 5 * time.Second
)

// resourceSampleScript is one adb shell round trip: CPU totals, memory, CPU
// identity, whatever GPU counters this vendor exposes, and every process.
const resourceSampleScript = `echo @@STAT; cat /proc/stat; echo @@MEM; cat /proc/meminfo; ` +
	`echo @@CPU; grep -c '^processor' /proc/cpuinfo; grep -m1 -i -e 'model name' -e 'Hardware' /proc/cpuinfo; ` +
	`echo @@GPU; cat /sys/class/kgsl/kgsl-3d0/gpubusy 2>/dev/null; cat /sys/class/kgsl/kgsl-3d0/gpu_busy_percentage 2>/dev/null; ` +
	`cat /sys/kernel/gpu/gpu_busy 2>/dev/null; cat /sys/class/devfreq/gpufreq/load 2>/dev/null; ` +
	`echo @@PROC; for d in /proc/[0-9]*; do echo @@P ${d##*/}; head -1 $d/stat 2>/dev/null; ` +
	`grep VmRSS $d/status 2>/dev/null; tr '\0' ' ' < $d/cmdline 2>/dev/null; echo; done`

// SampleResources reads the device once and stores the result. It binds the
// endpoint it started with, so a reconnect mid-read throws the reading away
// instead of ascribing it to the next channel.
func (m *Manager) SampleResources(ctx context.Context, id uuid.UUID) (ResourceSnapshot, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return ResourceSnapshot{}, err
	}

	m.mu.Lock()
	previous := m.sampleCounters[id]
	m.mu.Unlock()

	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", resourceSampleScript},
		Timeout: resourceSampleTimeout,
	}, nil)
	if err != nil {
		return ResourceSnapshot{}, sanitizeError(err)
	}
	if res.Cancelled {
		return ResourceSnapshot{}, ErrCancelled
	}
	if res.TimedOut {
		return m.keepPreviousSnapshot(id, "采样超时")
	}
	if res.ExitCode != 0 {
		return ResourceSnapshot{}, sanitizeError(Error(strings.TrimSpace(Redact(res.Output, ""))))
	}
	if !m.targetIsCurrent(id, target) {
		return ResourceSnapshot{}, ErrCancelled
	}

	snapshot := m.buildSnapshot(id, previous, parseResourceOutput(res.Output))
	if renderer := m.fetchRenderer(ctx, id, target); renderer != "" {
		snapshot.GPU.Renderer = renderer
	}
	if !snapshot.GPU.Available {
		snapshot.Warnings = append(snapshot.Warnings, WarningNoGPU)
	}
	snapshot = m.attachAppMeta(snapshot, id, target)

	if !m.targetIsCurrent(id, target) {
		return ResourceSnapshot{}, ErrCancelled
	}
	m.mu.Lock()
	m.resources[id] = snapshot
	m.notify()
	m.mu.Unlock()
	return snapshot, nil
}

// Resources returns the last reading, and whether there is one.
func (m *Manager) Resources(id uuid.UUID) (ResourceSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, ok := m.resources[id]
	if !ok {
		return ResourceSnapshot{}, false
	}
	snapshot.Apps = append([]AppUsage(nil), snapshot.Apps...)
	return snapshot, true
}

// keepPreviousSnapshot reports a failed round without discarding the last good
// numbers: a timeout is a reason to say so, not to blank the page.
func (m *Manager) keepPreviousSnapshot(id uuid.UUID, reason string) (ResourceSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, ok := m.resources[id]
	if !ok {
		return ResourceSnapshot{}, Error(reason + "，请稍后重试")
	}
	if !containsText(snapshot.Warnings, reason) {
		snapshot.Warnings = append(snapshot.Warnings, reason)
	}
	m.resources[id] = snapshot
	m.notify()
	return snapshot, nil
}

func containsText(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// buildSnapshot turns one parsed reading into what the page shows, using the
// previous round's counters for the deltas.
func (m *Manager) buildSnapshot(id uuid.UUID, previous *resourceCounters, parsed parsedSample) ResourceSnapshot {
	snapshot := ResourceSnapshot{At: time.Now()}
	counters := &resourceCounters{procs: map[int]procCounters{}}

	var deltaTotal uint64
	if parsed.cpuOK && previous != nil && previous.ok && parsed.cpu.Total > previous.cpu.Total {
		deltaTotal = parsed.cpu.Total - previous.cpu.Total
		deltaIdle := uint64(0)
		if parsed.cpu.Idle > previous.cpu.Idle {
			deltaIdle = parsed.cpu.Idle - previous.cpu.Idle
		}
		if deltaTotal > 0 && deltaIdle <= deltaTotal {
			snapshot.CPU.Available = true
			snapshot.CPU.Percent = 100 * float64(deltaTotal-deltaIdle) / float64(deltaTotal)
		}
	}
	snapshot.CPU.Model = parsed.model
	snapshot.CPU.Cores = parsed.cores
	if parsed.cpuOK {
		counters.cpu, counters.ok = parsed.cpu, true
	}

	if parsed.memOK && parsed.mem.Total > 0 {
		used := uint64(0)
		if parsed.mem.Total > parsed.mem.Available {
			used = parsed.mem.Total - parsed.mem.Available
		}
		snapshot.Memory = MemorySample{
			Total:     parsed.mem.Total,
			Available: parsed.mem.Available,
			Used:      used,
			Percent:   100 * float64(used) / float64(parsed.mem.Total),
			OK:        true,
		}
	}

	snapshot.GPU.Percent = parsed.gpuPercent
	snapshot.GPU.Available = parsed.gpuOK

	cpuPercent := map[int]float64{}
	for _, entry := range parsed.entries {
		counters.procs[entry.PID] = procCounters{jiffies: entry.Jiffies, start: entry.StartTicks}
		if !snapshot.CPU.Available || previous == nil || deltaTotal == 0 {
			continue
		}
		was, ok := previous.procs[entry.PID]
		// A reused PID is a different process; its counters have no delta.
		if !ok || was.start != entry.StartTicks || entry.Jiffies < was.jiffies {
			continue
		}
		cpuPercent[entry.PID] = 100 * float64(entry.Jiffies-was.jiffies) / float64(deltaTotal)
	}
	snapshot.Apps = GroupProcesses(parsed.entries, cpuPercent)

	m.mu.Lock()
	m.sampleCounters[id] = counters
	m.mu.Unlock()
	return snapshot
}

// fetchRenderer reads the GPU model once per connection: it never changes, and
// dumpsys is far too slow to run every two seconds.
func (m *Manager) fetchRenderer(ctx context.Context, id uuid.UUID, target operationTarget) string {
	m.mu.Lock()
	info := m.hardware[id]
	m.mu.Unlock()
	if info != nil && info.fetched {
		return info.renderer
	}

	renderer := ""
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", "dumpsys SurfaceFlinger"},
		Timeout: hardwareTimeout,
	}, nil)
	if err == nil && res.ExitCode == 0 {
		renderer = parseGLESRenderer(res.Output)
	}

	m.mu.Lock()
	if m.hardware[id] == nil {
		m.hardware[id] = &hardwareInfo{}
	}
	m.hardware[id].renderer, m.hardware[id].fetched = renderer, true
	m.mu.Unlock()
	return renderer
}

// attachAppMeta fills in the names and icons the cache knows and queues the
// rest. It runs outside the manager lock, because the cache has its own and a
// queued package must never block the window.
func (m *Manager) attachAppMeta(snapshot ResourceSnapshot, id uuid.UUID, target operationTarget) ResourceSnapshot {
	m.mu.Lock()
	source := m.appMeta
	m.mu.Unlock()
	if source == nil || len(snapshot.Apps) == 0 {
		return snapshot
	}
	device := deviceKey(id)
	pkgs := make([]string, 0, len(snapshot.Apps))
	for i := range snapshot.Apps {
		pkg := snapshot.Apps[i].Package
		pkgs = append(pkgs, pkg)
		meta, known := source.Lookup(device, pkg)
		if !known {
			continue
		}
		snapshot.Apps[i].Label = meta.Label
		snapshot.Apps[i].HasIcon = len(meta.Icon) > 0
		snapshot.Apps[i].MetaStale = meta.Stale
	}
	source.Request(device, target.ADB, target.Serial, pkgs)
	return snapshot
}

// deviceKey is the per-device identity the metadata cache keys by; the id
// survives reconnects, which a serial does not.
func deviceKey(id uuid.UUID) string { return id.String() }

// parsedSample is one reading of the sampling script, before the deltas.
type parsedSample struct {
	cpu        cpuTimes
	cpuOK      bool
	mem        memInfo
	memOK      bool
	cores      int
	model      string
	gpuPercent float64
	gpuOK      bool
	entries    []procEntry
}

type procRaw struct {
	pid, stat, status, cmdline string
}

// parseResourceOutput reads the sectioned output of resourceSampleScript.
func parseResourceOutput(text string) parsedSample {
	var out parsedSample
	var memLines []string
	section := ""
	var current *procRaw

	flush := func() {
		if current == nil {
			return
		}
		pid, err := strconv.Atoi(current.pid)
		if err == nil {
			if entry, ok := parseProcEntry(pid, current.stat, current.status, current.cmdline); ok {
				out.entries = append(out.entries, entry)
			}
		}
		current = nil
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if rest, ok := strings.CutPrefix(line, "@@P "); ok {
			flush()
			current = &procRaw{pid: strings.TrimSpace(rest)}
			continue
		}
		if strings.HasPrefix(line, "@@") {
			flush()
			section = line
			continue
		}
		switch section {
		case "@@STAT":
			if !out.cpuOK {
				if times, ok := parseCPUStat(line); ok {
					out.cpu, out.cpuOK = times, true
				}
			}
		case "@@MEM":
			memLines = append(memLines, line)
		case "@@CPU":
			if out.cores == 0 {
				if cores, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
					out.cores = cores
				}
			} else if out.model == "" {
				out.model = cpuModelFromLine(line)
			}
		case "@@GPU":
			if !out.gpuOK {
				if percent, ok := parseGPUBusy(line); ok {
					out.gpuPercent, out.gpuOK = percent, true
				}
			}
		case "@@PROC":
			if current == nil {
				continue
			}
			switch {
			case current.stat == "":
				current.stat = line
			case current.status == "" && strings.HasPrefix(line, "VmRSS:"):
				current.status = line
			case current.cmdline == "":
				current.cmdline = line
			}
		}
	}
	flush()

	if !out.memOK {
		if mem, ok := parseMemInfo(strings.Join(memLines, "\n")); ok {
			out.mem, out.memOK = mem, true
		}
	}
	return out
}

// cpuModelFromLine reads /proc/cpuinfo spellings like
// "Hardware\t: Qualcomm Technologies, Inc SM8150".
func cpuModelFromLine(line string) string {
	_, rest, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

// StartResourceSampling re-reads the device every resourceInterval until
// StopResourceSampling, so the page costs nothing while it is not open. It is
// safe to call on every frame.
func (m *Manager) StartResourceSampling(id uuid.UUID) {
	m.mu.Lock()
	if _, running := m.samplers[id]; running {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.samplers[id] = cancel
	interval := m.resourceInterval
	generation := m.generations[id]
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			m.mu.Lock()
			current := m.isCurrentLocked(id, generation)
			m.mu.Unlock()
			if !current {
				return
			}
			if _, err := m.SampleResources(ctx, id); err != nil {
				// A device that went away ends the loop; a transient busy
				// device just misses this round.
				m.mu.Lock()
				current := m.isCurrentLocked(id, generation)
				m.mu.Unlock()
				if !current {
					return
				}
			}
		}
	}()
}

// StopResourceSampling ends the loop and forgets what it read, so a device that
// comes back cannot show the previous channel's numbers.
func (m *Manager) StopResourceSampling(id uuid.UUID) {
	m.mu.Lock()
	if _, running := m.samplers[id]; !running {
		if _, stored := m.resources[id]; !stored {
			m.mu.Unlock()
			return
		}
	}
	m.stopResourceSamplingLocked(id)
	delete(m.resources, id)
	delete(m.sampleCounters, id)
	m.notify()
	m.mu.Unlock()
}

func (m *Manager) stopResourceSamplingLocked(id uuid.UUID) {
	if cancel, ok := m.samplers[id]; ok {
		cancel()
		delete(m.samplers, id)
	}
}

// RefreshAppMeta reads the names and icons of the applications the last
// reading saw again, ignoring the cache lifetime. It returns as soon as the
// work is queued: the page refreshes when the metadata arrives.
func (m *Manager) RefreshAppMeta(ctx context.Context, id uuid.UUID) error {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return err
	}

	m.mu.Lock()
	source := m.appMeta
	snapshot, seen := m.resources[id]
	m.mu.Unlock()
	if source == nil || !seen {
		return nil
	}
	pkgs := make([]string, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		pkgs = append(pkgs, app.Package)
	}
	if len(pkgs) == 0 {
		return nil
	}
	source.Refresh(deviceKey(id), target.ADB, target.Serial, pkgs)
	return nil
}
