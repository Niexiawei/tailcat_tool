package core

import (
	"context"
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
	sourceKey, kind := cfg.Source, cfg.Kind
	if cfg.Kind == RelayURL {
		// The network call happens before the lock: a slow server must not
		// stall every other manager call.
		cfg, err = fetchRelayMap(ctx, cfg.URL)
		if err != nil {
			return RelayMapPreview{}, err
		}
	}
	preview := RelayMapPreview{
		Source:    sourceKey,
		Kind:      kind,
		JSON:      cfg.Source,
		Map:       cfg.Map,
		FetchedAt: time.Now(),
	}
	m.mu.Lock()
	if m.relayPreviews == nil {
		m.relayPreviews = map[string]RelayMapPreview{}
	}
	m.relayPreviews[sourceKey] = preview
	m.mu.Unlock()
	return preview, nil
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
