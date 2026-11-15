package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Configuration is the whole persisted app state. The JSON shape matches the
// Swift build so an existing configuration.json keeps working; the "tailcat"
// tool path it used to hold is ignored and not written back.
type Configuration struct {
	Profiles     []Profile     `json:"profiles"`
	Tools        ToolSettings  `json:"tools"`
	RelayPresets []RelayPreset `json:"relayPresets"`
}

// ConfigStore reads and writes configuration.json inside a directory.
type ConfigStore struct {
	dir string
}

// NewConfigStore returns a store rooted at dir.
func NewConfigStore(dir string) *ConfigStore { return &ConfigStore{dir: dir} }

// Dir is the directory the configuration file lives in.
func (s *ConfigStore) Dir() string { return s.dir }

// Path is the configuration file path.
func (s *ConfigStore) Path() string { return filepath.Join(s.dir, "configuration.json") }

// Load reads the configuration. A missing file is not an error: it yields the
// defaults, which is how a first launch behaves.
func (s *ConfigStore) Load() (Configuration, error) {
	var cfg Configuration
	if err := rejectSymlink(s.dir); err != nil {
		return cfg, err
	}
	path := s.Path()
	if err := rejectSymlink(path); err != nil {
		return cfg, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Configuration{}, Errorf("配置文件无法解析：%v", err)
	}
	inferDirectMode(&cfg)
	return cfg, nil
}

// inferDirectMode repairs a direct device whose flag did not survive a save
// (older builds wrote only the target) and drops the remote port such a device
// never uses. It is idempotent, so no migration step is needed.
func inferDirectMode(cfg *Configuration) {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].DirectTarget != "" && !cfg.Profiles[i].DirectADB {
			cfg.Profiles[i].DirectADB = true
		}
		if cfg.Profiles[i].DirectADB {
			cfg.Profiles[i].RemotePort = 0
			cfg.Profiles[i].PreferredPort = nil
		}
	}
}

// Save writes the configuration atomically with owner-only permissions.
func (s *ConfigStore) Save(cfg Configuration) error {
	if err := rejectSymlink(s.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return err
	}
	path := s.Path()
	if err := rejectSymlink(path); err != nil {
		return err
	}
	// Always write arrays, never null, so the file stays diff-friendly and
	// matches what the Swift build produced.
	if cfg.Profiles == nil {
		cfg.Profiles = []Profile{}
	}
	if cfg.RelayPresets == nil {
		cfg.RelayPresets = []RelayPreset{}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(s.dir, ".configuration-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

// rejectSymlink refuses to read or write through a symbolic link.
func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Errorf("为保护配置，拒绝读取或覆盖符号链接：%s", filepath.Base(path))
	}
	return nil
}
