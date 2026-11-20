package core

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"tailscale.com/tailcfg"
)

// RelayPresetUsage names the devices that use a preset. Temporary devices count:
// they hold a Source like any other device while the app runs.
//
// Devices store the source, not the preset ID, so the comparison is on Source:
// two presets pointing at the same map are both "used" by such a device.
func (m *Manager) RelayPresetUsage(id uuid.UUID) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, preset := range m.relayPresets {
		if preset.ID == id {
			return m.relayUsersLocked(preset.Source)
		}
	}
	return nil
}

// relayUsersLocked lists the devices whose RelaySource is source, saved devices
// first, temporary ones after — the sidebar's order.
func (m *Manager) relayUsersLocked(source string) []string {
	names := make([]string, 0, 2)
	for _, profile := range m.profiles {
		if profile.RelaySource == source {
			names = append(names, profile.Name)
		}
	}
	for _, profile := range m.temporary {
		if profile.RelaySource == source {
			names = append(names, profile.Name)
		}
	}
	return names
}

// DeleteRelayPreset removes a preset no device uses. It refuses rather than
// rewriting device configuration behind the user's back.
func (m *Manager) DeleteRelayPreset(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := -1
	for i := range m.relayPresets {
		if m.relayPresets[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Error("没有找到这个 DERP 节点，它可能已被删除")
	}
	preset := m.relayPresets[index]
	if users := m.relayUsersLocked(preset.Source); len(users) > 0 {
		return Errorf("有 %d 台设备正在使用「%s」，请先把它们改用其他 DERP 节点", len(users), preset.Name)
	}

	updated := make([]RelayPreset, 0, len(m.relayPresets)-1)
	updated = append(updated, m.relayPresets[:index]...)
	updated = append(updated, m.relayPresets[index+1:]...)
	saved := m.relayPresets
	m.relayPresets = updated
	if err := m.persistLocked(); err != nil {
		m.relayPresets = saved
		return err
	}
	m.notify()
	return nil
}

// RelayMapPreview is one DERP source as the detail page shows it.
type RelayMapPreview struct {
	Source string
	Kind   RelayKind
	// JSON is the canonical map text: parsed locally for an inline source and
	// downloaded for a URL.
	JSON      string
	Map       *tailcfg.DERPMap
	FetchedAt time.Time
}

// RelayMapPreview loads one source now: inline sources parse locally, URLs are
// downloaded. The result is kept in memory only, because a map changes and a
// stale copy would look current.
func (m *Manager) RelayMapPreview(ctx context.Context, source string) (RelayMapPreview, error) {
	cfg, err := NormalizeRelay(source)
	if err != nil {
		return RelayMapPreview{}, err
	}
	if cfg.IsOfficial() {
		return RelayMapPreview{}, Error("默认节点由 tailcat 提供，这里没有可预览的副本")
	}
	return m.loadRelayMap(ctx, source)
}

// loadRelayMap loads one source's map and remembers it as the preview the
// detail page shows. The probe cycle uses it too, so a probed map is free to
// look at.
func (m *Manager) loadRelayMap(ctx context.Context, source string) (RelayMapPreview, error) {
	cfg, err := NormalizeRelay(source)
	if err != nil {
		return RelayMapPreview{}, err
	}
	key, kind := cfg.Source, cfg.Kind
	if cfg.IsOfficial() {
		// The default list has no local source of its own: tailcat's file is
		// fetched, decoded, and kept in memory like any other URL.
		cfg, err = fetchRelayMap(ctx, m.defaultNodeListURL)
		if err != nil {
			return RelayMapPreview{}, err
		}
	} else if cfg.Kind == RelayURL {
		// The download happens before the lock: a slow server must not stall
		// every other manager call.
		cfg, err = fetchRelayMap(ctx, cfg.URL)
		if err != nil {
			return RelayMapPreview{}, err
		}
	}
	preview := RelayMapPreview{
		Source:    key,
		Kind:      kind,
		JSON:      cfg.Source,
		Map:       cfg.Map,
		FetchedAt: time.Now(),
	}
	m.mu.Lock()
	if m.relayPreviews == nil {
		m.relayPreviews = map[string]RelayMapPreview{}
	}
	m.relayPreviews[key] = preview
	m.mu.Unlock()
	return preview, nil
}

// RelayProbe answers with the last cycle's result for a source; ok is false
// before the first cycle. An empty source is the default node list.
//
// The map is read first and the source only normalized on a miss: the page asks
// once per row per frame, and normalizing an inline map is a JSON parse plus a
// marshal of up to 128 KiB.
func (m *Manager) RelayProbe(source string) (RelayProbe, bool) {
	if probe, ok := m.relayProbeCopy(source); ok {
		return probe, true
	}
	cfg, err := NormalizeRelay(source)
	if err != nil || cfg.Source == source {
		return RelayProbe{}, false
	}
	return m.relayProbeCopy(cfg.Source)
}

// relayProbeCopy reads one stored result, handing the caller its own slice: the
// UI reads it while the next cycle writes the manager's copy.
func (m *Manager) relayProbeCopy(key string) (RelayProbe, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	probe, ok := m.relayProbes[key]
	if !ok {
		return RelayProbe{}, false
	}
	probe.Nodes = append([]RelayNodeProbe(nil), probe.Nodes...)
	return probe, true
}

// RelayProbeRunning reports whether a cycle is in flight.
func (m *Manager) RelayProbeRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.relayProbeRunning
}

// RefreshRelayProbes starts a cycle now. A second call while one runs returns
// at once: a timer and a button must not queue up behind each other.
func (m *Manager) RefreshRelayProbes() {
	m.mu.Lock()
	if m.relayProbeRunning {
		m.mu.Unlock()
		return
	}
	m.relayProbeRunning = true
	m.mu.Unlock()
	// The window is told before the work starts, so the spinner appears on the
	// frame the button was pressed in.
	m.notify()
	go m.probeCycle()
}

// relayProbeLoop probes every node on an interval until the manager shuts
// down. The first cycle runs immediately.
func (m *Manager) relayProbeLoop() {
	m.RefreshRelayProbes()
	ticker := time.NewTicker(m.relayProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.RefreshRelayProbes()
		}
	}
}

// probeCycle runs one cycle: every source's map, then every node in it. The
// network happens off the lock; results land in one write at the end, because
// thirty rows do not need thirty redraws.
func (m *Manager) probeCycle() {
	defer func() {
		m.mu.Lock()
		m.relayProbeRunning = false
		m.mu.Unlock()
		m.notify()
	}()
	ctx, cancel := context.WithTimeout(m.ctx, relayProbeBudget)
	defer cancel()
	results := map[string]RelayProbe{}
	for _, source := range m.probeSources() {
		if ctx.Err() != nil {
			// Out of budget before this source even started. Leave it as it
			// was: an old answer beats a fabricated one.
			break
		}
		probe := m.probeSource(ctx, source)
		results[probe.Source] = probe
	}
	m.mu.Lock()
	if m.relayProbes == nil {
		m.relayProbes = map[string]RelayProbe{}
	}
	for source, probe := range results {
		m.relayProbes[source] = probe
	}
	m.mu.Unlock()
}

// probeSources lists what one cycle covers: the default node list first, then
// every saved preset. Two presets pointing at the same map are one entry.
func (m *Manager) probeSources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{"": true}
	sources := []string{""}
	for _, preset := range m.relayPresets {
		if seen[preset.Source] {
			continue
		}
		seen[preset.Source] = true
		sources = append(sources, preset.Source)
	}
	return sources
}

// probeSource probes every node of one source. A map that cannot be loaded is
// one error for the whole source, not a list of dead nodes.
func (m *Manager) probeSource(ctx context.Context, source string) RelayProbe {
	preview, err := m.loadRelayMap(ctx, source)
	if err != nil {
		// Key the failure by the source as it stands, falling back to its
		// canonical form: an unparsable source must not answer under "", which
		// is the default node list's key.
		key := source
		if cfg, normalizeErr := NormalizeRelay(source); normalizeErr == nil {
			key = cfg.Source
		}
		return RelayProbe{Source: key, Err: err.Error(), CheckedAt: time.Now()}
	}
	targets := relayProbeTargets(preview.Map)
	nodes := make([]RelayNodeProbe, len(targets))
	for i, target := range targets {
		nodes[i] = RelayNodeProbe{
			RegionID: target.RegionID,
			Name:     target.Name,
			Address:  relayAddress(target),
			STUNOnly: target.STUNOnly,
		}
	}
	m.probeTargets(ctx, targets, nodes)
	checked := time.Now()
	for i := range nodes {
		nodes[i].CheckedAt = checked
	}
	return RelayProbe{Source: preview.Source, Nodes: nodes, CheckedAt: checked}
}

// probeTargets runs the two checks for every node, eight at a time. A node the
// budget cut off keeps its empty checks, which the UI shows as 未检测.
func (m *Manager) probeTargets(ctx context.Context, targets []RelayProbeTarget, nodes []RelayNodeProbe) {
	workers := make(chan struct{}, relayProbeWorkers)
	var wg sync.WaitGroup
	for i := range targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case workers <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-workers }()
			httpCheck, stunCheck := m.relayNodeProber.ProbeNode(ctx, targets[i])
			nodes[i].HTTP, nodes[i].STUN = httpCheck, stunCheck
		}(i)
	}
	wg.Wait()
}

// relayProbeTargets flattens a map into what a probe needs, regions in ID order
// so a table and a test read the same sequence twice.
func relayProbeTargets(dm *tailcfg.DERPMap) []RelayProbeTarget {
	if dm == nil {
		return nil
	}
	ids := make([]tailcfg.DERPRegionID, 0, len(dm.Regions))
	for id := range dm.Regions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	targets := make([]RelayProbeTarget, 0, len(ids))
	for _, id := range ids {
		region := dm.Regions[id]
		if region == nil {
			continue
		}
		for _, node := range region.Nodes {
			if node == nil {
				continue
			}
			targets = append(targets, RelayProbeTarget{
				RegionID: node.RegionID,
				Name:     node.Name,
				HostName: node.HostName,
				CertName: node.CertName,
				DERPPort: node.DERPPort,
				STUNPort: node.STUNPort,
				STUNOnly: node.STUNOnly,
			})
		}
	}
	return targets
}

// CachedRelayMapPreview answers without the network. A failed fetch stores
// nothing, so the detail page's retry really retries.
func (m *Manager) CachedRelayMapPreview(source string) (RelayMapPreview, bool) {
	cfg, err := NormalizeRelay(source)
	if err != nil || cfg.Source == "" {
		return RelayMapPreview{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	preview, ok := m.relayPreviews[cfg.Source]
	return preview, ok
}

// RelayProbeCheckedAt is the newest time any source was probed; zero before the
// first cycle. The list header shows it so a count is never mistaken for a live
// reading.
func (m *Manager) RelayProbeCheckedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest time.Time
	for _, probe := range m.relayProbes {
		if probe.CheckedAt.After(newest) {
			newest = probe.CheckedAt
		}
	}
	return newest
}
