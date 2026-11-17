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
	fetched  bool
	cores    int
	model    string
	renderer string
}

// WarningNoGPU explains a device whose vendor exposes no GPU counter.
const WarningNoGPU = "该设备不提供 GPU 使用率"

const (
	// defaultResourceInterval is how often the open resource page re-reads.
	defaultResourceInterval = 2 * time.Second
	resourceSampleTimeout   = 8 * time.Second
	hardwareTimeout         = 5 * time.Second
)

// resourceSampleScript is one adb shell round trip: the CPU totals, memory,
// whichever vendor GPU counters exist, and every process. The per-process loop
// uses shell builtins only: a grep/head/tr per process costs about 14 ms each
// on a real device, which made a single round take 21 seconds.
const resourceSampleScript = `echo @@STAT; IFS= read -r line < /proc/stat; echo "$line"; ` +
	`echo @@MEM; cat /proc/meminfo; echo @@GPU; ` +
	`for f in /sys/class/kgsl/kgsl-3d0/gpubusy /sys/class/kgsl/kgsl-3d0/gpu_busy_percentage /sys/kernel/gpu/gpu_busy /sys/class/devfreq/gpufreq/load; do ` +
	`IFS= read -r line < $f 2>/dev/null && echo "$line"; done; ` +
	`echo @@PROC; for d in /proc/[0-9]*; do pid=${d#/proc/}; cmd=""; ` +
	`IFS= read -r cmd < $d/cmdline 2>/dev/null; [ -n "$cmd" ] || continue; ` +
	`stat=""; IFS= read -r stat < $d/stat 2>/dev/null || continue; [ -n "$stat" ] || continue; ` +
	`rss=0; while IFS= read -r line; do case $line in VmRSS:*) set -- $line; rss=$2; break;; esac; done < $d/status 2>/dev/null; ` +
	`echo "@@P $pid|$rss|$stat|$cmd"; done`

// hardwareScript reads what never changes on a connection: the CPU identity and
// the GPU model. It runs once, after the first reading is on screen.
const hardwareScript = `echo @@CPU; grep -c '^processor' /proc/cpuinfo; ` +
	`grep -m1 -i -e 'model name' -e 'Hardware' /proc/cpuinfo; ` +
	`echo @@GPU; dumpsys SurfaceFlinger | grep -m1 '^GLES:'`

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

	// The CPU and GPU identity never changes, so it is read once and only
	// after the numbers above are on screen: dumpsys is slow and would
	// otherwise hold the first reading back.
	m.ensureHardware(ctx, id, target)
	// ensureHardware fills the identity into the stored reading, so return that
	// rather than the copy taken before it ran.
	if updated, ok := m.Resources(id); ok {
		return updated, nil
	}
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
	if hardware := m.hardwareOf(id); hardware != nil {
		snapshot.CPU.Model = hardware.model
		snapshot.CPU.Cores = hardware.cores
		if hardware.renderer != "" {
			snapshot.GPU.Renderer = hardware.renderer
		}
	}
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

// hardwareOf returns the cached CPU and GPU identity, if it was read.
func (m *Manager) hardwareOf(id uuid.UUID) *hardwareInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hardware[id]
}

// ensureHardware reads the CPU identity and the GPU model once per connection.
// It runs after a reading is stored, and it updates that reading in place, so a
// slow dumpsys never delays the numbers.
func (m *Manager) ensureHardware(ctx context.Context, id uuid.UUID, target operationTarget) {
	if info := m.hardwareOf(id); info != nil && info.fetched {
		return
	}

	hardware := &hardwareInfo{fetched: true}
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", hardwareScript},
		Timeout: hardwareTimeout,
	}, nil)
	if err == nil && res.ExitCode == 0 {
		hardware.cores, hardware.model, hardware.renderer = parseHardwareOutput(res.Output)
	}

	m.mu.Lock()
	m.hardware[id] = hardware
	if snapshot, ok := m.resources[id]; ok {
		snapshot.CPU.Cores = hardware.cores
		snapshot.CPU.Model = hardware.model
		if hardware.renderer != "" {
			snapshot.GPU.Renderer = hardware.renderer
		}
		m.resources[id] = snapshot
	}
	m.notify()
	m.mu.Unlock()
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
	// Nothing is fetched here on purpose: reading a name costs a whole APK
	// pulled off the device, which is far too slow to do for every running
	// application. The page asks for one application at a time, or all of them
	// when the user presses refresh.
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
	gpuPercent float64
	gpuOK      bool
	entries    []procEntry
}

// parseResourceOutput reads the sectioned output of resourceSampleScript.
func parseResourceOutput(text string) parsedSample {
	var out parsedSample
	var memLines []string
	section := ""

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@P ") {
			if section != "@@PROC" {
				continue
			}
			// "@@P <pid>|<rss kB>|<stat line>|<cmdline>"
			parts := strings.SplitN(strings.TrimPrefix(line, "@@P "), "|", 4)
			if len(parts) < 4 {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				continue
			}
			status := ""
			if rss := strings.TrimSpace(parts[1]); rss != "" && rss != "0" {
				status = "VmRSS:\t" + rss + " kB"
			}
			if entry, ok := parseProcEntry(pid, parts[2], status, parts[3]); ok {
				out.entries = append(out.entries, entry)
			}
			continue
		}
		if strings.HasPrefix(line, "@@") {
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
		case "@@GPU":
			if !out.gpuOK {
				if percent, ok := parseGPUBusy(line); ok {
					out.gpuPercent, out.gpuOK = percent, true
				}
			}
		}
	}

	if !out.memOK {
		if mem, ok := parseMemInfo(strings.Join(memLines, "\n")); ok {
			out.mem, out.memOK = mem, true
		}
	}
	return out
}

// parseHardwareOutput reads hardwareScript's output: core count, CPU model and
// the GPU renderer.
func parseHardwareOutput(text string) (cores int, model, renderer string) {
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@") {
			section = line
			continue
		}
		switch section {
		case "@@CPU":
			if cores == 0 {
				if count, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
					cores = count
				}
			} else if model == "" {
				model = cpuModelFromLine(line)
			}
		case "@@GPU":
			if renderer == "" {
				renderer = parseGLESRenderer(line)
			}
		}
	}
	return cores, model, renderer
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

// StopResourceSampling ends the loop and keeps the last reading, so coming back
// to the page shows numbers at once instead of reading the device again.
// Disconnecting still forgets everything.
func (m *Manager) StopResourceSampling(id uuid.UUID) {
	m.mu.Lock()
	if _, running := m.samplers[id]; !running {
		m.mu.Unlock()
		return
	}
	m.stopResourceSamplingLocked(id)
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
// work is queued: the page refreshes when the metadata arrives. Processes the
// device already said have no APK are skipped: asking again cannot help and
// costs a round trip each.
func (m *Manager) RefreshAppMeta(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	snapshot, seen := m.resources[id]
	source := m.appMeta
	m.mu.Unlock()
	if !seen {
		return nil
	}
	pkgs := make([]string, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		if source != nil {
			if status, known := source.Status(deviceKey(id), app.Package); known && status.Final {
				continue
			}
		}
		pkgs = append(pkgs, app.Package)
	}
	return m.ResolveAppMeta(ctx, id, pkgs)
}

// ResolveAppMeta queues a read of the given packages' names and icons,
// ignoring what the cache holds. It returns as soon as the work is queued.
func (m *Manager) ResolveAppMeta(ctx context.Context, id uuid.UUID, pkgs []string) error {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return nil
	}

	m.mu.Lock()
	source := m.appMeta
	m.mu.Unlock()
	if source == nil {
		return nil
	}
	source.Refresh(deviceKey(id), target.ADB, target.Serial, pkgs)
	return nil
}

// AppMeta returns the cached name and icon of one package, for the views that
// draw them.
func (m *Manager) AppMeta(id uuid.UUID, pkg string) (AppMeta, bool) {
	m.mu.Lock()
	source := m.appMeta
	m.mu.Unlock()
	if source == nil {
		return AppMeta{}, false
	}
	return source.Lookup(deviceKey(id), pkg)
}

// AppMetaStatus reports whether a package's name is being read, is known, or
// failed to read, and why. The view draws its spinner from this instead of
// waiting on a timer it cannot verify.
func (m *Manager) AppMetaStatus(id uuid.UUID, pkg string) (AppMetaStatus, bool) {
	m.mu.Lock()
	source := m.appMeta
	m.mu.Unlock()
	if source == nil {
		return AppMetaStatus{}, false
	}
	return source.Status(deviceKey(id), pkg)
}
