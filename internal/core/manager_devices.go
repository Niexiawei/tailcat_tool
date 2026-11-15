package core

import (
	"context"
	"os"
	"strings"

	"github.com/google/uuid"
)

// SaveDeviceRequest is one device as the editor submits it.
type SaveDeviceRequest struct {
	ID            *uuid.UUID
	Name          string
	Address       string
	PreferredPort *int
	RemotePort    int
	RelaySource   string
}

// SaveTemporaryDeviceRequest is one throwaway device as the editor submits it.
type SaveTemporaryDeviceRequest = SaveDeviceRequest

// SaveRelayPresetRequest is one DERP preset as the editor submits it.
type SaveRelayPresetRequest struct {
	ID     *uuid.UUID
	Name   string
	Source string
}

// validatedDeviceInput trims and checks the editor's fields.
func validatedDeviceInput(name, address string, preferred *int, remote int) (string, string, *int, error) {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name == "" || len([]rune(name)) > 80 {
		return "", "", nil, Error("设备名称需要 1–80 个字符")
	}
	if !Plausible(address) {
		return "", "", nil, Error("请填写以 tc 开头的完整 Tailcat 连接地址")
	}
	if remote < 1 || remote > 65535 {
		return "", "", nil, Error("端口必须在 1–65535 之间；本地端口留空表示自动分配")
	}
	if preferred != nil && (*preferred < 1 || *preferred > 65535) {
		return "", "", nil, Error("端口必须在 1–65535 之间；本地端口留空表示自动分配")
	}
	return name, address, preferred, nil
}

func (m *Manager) persistLocked() error {
	if m.config == nil {
		return nil
	}
	return m.config.Save(Configuration{
		Profiles:     m.profiles,
		Tools:        m.tools,
		RelayPresets: m.relayPresets,
	})
}

// SaveDevice creates or updates a saved device.
func (m *Manager) SaveDevice(req SaveDeviceRequest) (uuid.UUID, error) {
	name, address, preferred, err := validatedDeviceInput(req.Name, req.Address, req.PreferredPort, req.RemotePort)
	if err != nil {
		return uuid.Nil, err
	}
	relay, err := NormalizeRelay(req.RelaySource)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	if req.ID != nil {
		id = *req.ID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isTemporaryLocked(id) {
		return uuid.Nil, Error("请使用“保存到我的设备”保存临时会话")
	}
	if session := m.sessionLocked(id); session.Serial != "" || session.State.Busy() {
		return uuid.Nil, Error("请先断开该设备，再修改连接配置")
	}

	previousAddress, hadAddress, err := m.credentials.Read(id)
	if err != nil {
		return uuid.Nil, err
	}
	profile := Profile{ID: id, Name: name}
	if existing, ok := m.profileLocked(id); ok {
		profile = existing
		profile.Name = name
	}
	profile.PreferredPort = preferred
	profile.RemotePort = req.RemotePort
	profile.RelaySource = relay.Source

	updated := append([]Profile(nil), m.profiles...)
	replaced := false
	for i := range updated {
		if updated[i].ID == id {
			updated[i] = profile
			replaced = true
		}
	}
	if !replaced {
		updated = append(updated, profile)
	}

	if err := m.credentials.Write(address, id); err != nil {
		return uuid.Nil, err
	}
	saved := m.profiles
	m.profiles = updated
	if err := m.persistLocked(); err != nil {
		m.profiles = saved
		if hadAddress {
			_ = m.credentials.Write(previousAddress, id)
		} else {
			_ = m.credentials.Delete(id)
		}
		return uuid.Nil, err
	}
	if _, ok := m.sessions[id]; !ok {
		m.sessions[id] = NewSession()
	}
	m.notify()
	return id, nil
}

// SaveTemporaryDevice creates or updates a device that lives only in memory.
func (m *Manager) SaveTemporaryDevice(req SaveDeviceRequest) (uuid.UUID, error) {
	suppliedName := strings.TrimSpace(req.Name)
	id := uuid.New()
	if req.ID != nil {
		id = *req.ID
	}
	defaultName := "临时设备 " + itoaSmall(m.nextTemporaryNumber)
	if req.ID != nil {
		for _, p := range m.temporary {
			if p.ID == *req.ID {
				defaultName = p.Name
			}
		}
	}
	if suppliedName == "" {
		suppliedName = defaultName
	}
	name, address, preferred, err := validatedDeviceInput(suppliedName, req.Address, req.PreferredPort, req.RemotePort)
	if err != nil {
		return uuid.Nil, err
	}
	relay, err := NormalizeRelay(req.RelaySource)
	if err != nil {
		return uuid.Nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if req.ID != nil {
		if !m.isTemporaryLocked(id) {
			return uuid.Nil, Error("临时设备已关闭或已保存")
		}
		if session := m.sessionLocked(id); session.Serial != "" || session.State.Busy() {
			return uuid.Nil, Error("请先断开该临时设备，再修改连接配置")
		}
	}
	profile := Profile{ID: id, Name: name, PreferredPort: preferred, RemotePort: req.RemotePort, RelaySource: relay.Source}
	m.tempCredentials[id] = address
	replaced := false
	for i := range m.temporary {
		if m.temporary[i].ID == id {
			m.temporary[i] = profile
			replaced = true
		}
	}
	if !replaced {
		m.temporary = append(m.temporary, profile)
		if _, ok := m.sessions[id]; !ok {
			m.sessions[id] = NewSession()
			m.nextTemporaryNumber++
		}
	}
	m.notify()
	return id, nil
}

// PromoteTemporaryDevice moves a temporary device into the saved list without
// touching its live channel.
func (m *Manager) PromoteTemporaryDevice(id uuid.UUID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var profile *Profile
	for i := range m.temporary {
		if m.temporary[i].ID == id {
			profile = &m.temporary[i]
		}
	}
	address, hasAddress := m.tempCredentials[id]
	if profile == nil || !hasAddress {
		return Error("临时设备已关闭或已保存")
	}
	if m.sessions[id].State == StateDisconnecting {
		return Error("临时设备正在关闭，请稍后再保存")
	}
	validatedName, _, _, err := validatedDeviceInput(name, address, profile.PreferredPort, profile.RemotePort)
	if err != nil {
		return err
	}
	moved := *profile
	moved.Name = validatedName

	if err := m.credentials.Write(address, id); err != nil {
		return err
	}
	updated := append(append([]Profile(nil), m.profiles...), moved)
	saved := m.profiles
	m.profiles = updated
	if err := m.persistLocked(); err != nil {
		m.profiles = saved
		_ = m.credentials.Delete(id)
		return err
	}
	remaining := make([]Profile, 0, len(m.temporary))
	for _, p := range m.temporary {
		if p.ID != id {
			remaining = append(remaining, p)
		}
	}
	m.temporary = remaining
	delete(m.tempCredentials, id)
	m.notice = "已保存到我的设备，当前会话保持"
	m.notify()
	return nil
}

// RemoveDevice disconnects and forgets a device.
func (m *Manager) RemoveDevice(ctx context.Context, id uuid.UUID) error {
	m.Disconnect(ctx, id)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isTemporaryLocked(id) {
		remaining := make([]Profile, 0, len(m.temporary))
		for _, p := range m.temporary {
			if p.ID != id {
				remaining = append(remaining, p)
			}
		}
		m.temporary = remaining
		delete(m.tempCredentials, id)
		delete(m.sessions, id)
		delete(m.generations, id)
		delete(m.retryCounts, id)
		delete(m.nextRetries, id)
		m.notify()
		return nil
	}
	updated := make([]Profile, 0, len(m.profiles))
	for _, p := range m.profiles {
		if p.ID != id {
			updated = append(updated, p)
		}
	}
	saved := m.profiles
	m.profiles = updated
	if err := m.persistLocked(); err != nil {
		m.profiles = saved
		return err
	}
	delete(m.sessions, id)
	delete(m.generations, id)
	delete(m.retryCounts, id)
	delete(m.nextRetries, id)
	if err := m.credentials.Delete(id); err != nil {
		return err
	}
	m.notify()
	return nil
}

// UpdateTools stores new tool paths. It refuses while a channel is live so a
// running session keeps the paths it started with.
func (m *Manager) UpdateTools(settings ToolSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ownedActiveLocked() > 0 {
		return Error("请先断开应用管理的连接，再修改工具路径")
	}
	for _, s := range m.sessions {
		if s.State.Busy() {
			return Error("请先断开应用管理的连接，再修改工具路径")
		}
	}
	saved := m.tools
	m.tools = settings
	if err := m.persistLocked(); err != nil {
		m.tools = saved
		return err
	}
	m.notify()
	return nil
}

// ConfigureInstalledTool stores the path of a freshly installed tool.
func (m *Manager) ConfigureInstalledTool(component ToolComponent, path string) error {
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return Errorf("未找到 %s，请在设置中指定可执行文件的绝对路径", component.Label())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	saved := m.tools
	m.tools.Set(component, path)
	if err := m.persistLocked(); err != nil {
		m.tools = saved
		return err
	}
	m.notify()
	return nil
}

// SaveRelayPreset creates or updates a DERP preset.
func (m *Manager) SaveRelayPreset(req SaveRelayPresetRequest) (uuid.UUID, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 80 {
		return uuid.Nil, Error("预设名称需要 1–80 个字符")
	}
	relay, err := NormalizeRelay(req.Source)
	if err != nil {
		return uuid.Nil, Error("请填写预设的 JSON 链接或内容")
	}
	if relay.IsOfficial() {
		return uuid.Nil, Error("请填写预设的 JSON 链接或内容")
	}
	id := uuid.New()
	if req.ID != nil {
		id = *req.ID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, preset := range m.relayPresets {
		if preset.ID != id && preset.Name == name {
			return uuid.Nil, Error("已有同名预设，请修改名称或选择该预设后更新")
		}
	}
	preset := RelayPreset{ID: id, Name: name, Source: relay.Source}
	updated := append([]RelayPreset(nil), m.relayPresets...)
	replaced := false
	for i := range updated {
		if updated[i].ID == id {
			updated[i] = preset
			replaced = true
		}
	}
	if !replaced {
		updated = append(updated, preset)
	}
	saved := m.relayPresets
	m.relayPresets = updated
	if err := m.persistLocked(); err != nil {
		m.relayPresets = saved
		return uuid.Nil, err
	}
	m.notify()
	return id, nil
}

// OwnedActiveCount is how many app-managed channels are live.
func (m *Manager) OwnedActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownedActiveLocked()
}

func (m *Manager) ownedActiveLocked() int {
	count := 0
	for _, s := range m.sessions {
		if s.Serial != "" {
			count++
		}
	}
	return count
}

func itoaSmall(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
