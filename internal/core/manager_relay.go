package core

import "github.com/google/uuid"

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
		return Error("没有找到这个 DERP 地图，它可能已被删除")
	}
	preset := m.relayPresets[index]
	if users := m.relayUsersLocked(preset.Source); len(users) > 0 {
		return Errorf("有 %d 台设备正在使用「%s」，请先把它们改用其他 DERP 地图", len(users), preset.Name)
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
