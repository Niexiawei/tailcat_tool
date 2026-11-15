package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/platform"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// maxIdentityFileSize bounds how much we read from an identity file.
const maxIdentityFileSize = 4096

// identityLockWait is how long one identity initialization waits for another.
const identityLockWait = 5 * time.Second

var (
	identityNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)
	publicKeyPattern    = regexp.MustCompile(`^nodekey:[0-9a-f]{64}$`)
)

// Identity is a device's stable Tailcat client identity on disk.
type Identity struct {
	// File is the path of the .private.json file.
	File string
	// PublicKey is "nodekey:<64 hex>", the value a server allowlists.
	PublicKey string
}

// IdentityStore owns the per-device identity files. It never regenerates a
// file that already exists: a saved identity is the only way a device stays
// allowlisted, so a corrupt file is an error, not a reason to rotate keys.
type IdentityStore struct {
	dir string
	// newKey is replaceable in tests to count generations.
	newKey func() *tailcat.PrivateKey
}

// NewIdentityStore returns a store rooted at dir.
func NewIdentityStore(dir string) *IdentityStore {
	return &IdentityStore{dir: dir, newKey: tailcat.NewPrivateKey}
}

// Dir is where the identity files live.
func (s *IdentityStore) Dir() string { return s.dir }

func (s *IdentityStore) validateName(name string) error {
	if !identityNamePattern.MatchString(name) {
		return Error("客户端身份名称无效")
	}
	return nil
}

func (s *IdentityStore) filePath(name string) string {
	return filepath.Join(s.dir, name+".private.json")
}

// Identity returns the device identity, creating the key file on first use.
func (s *IdentityStore) Identity(ctx context.Context, name string) (Identity, error) {
	id, _, err := s.load(ctx, name)
	return id, err
}

// NodeKey returns the private key the tailcat client needs.
func (s *IdentityStore) NodeKey(ctx context.Context, name string) (key.NodePrivate, error) {
	_, priv, err := s.load(ctx, name)
	return priv, err
}

func (s *IdentityStore) load(ctx context.Context, name string) (Identity, key.NodePrivate, error) {
	var zero Identity
	if err := s.validateName(name); err != nil {
		return zero, key.NodePrivate{}, err
	}
	parent := filepath.Dir(s.dir)
	if err := rejectSymlink(parent); err != nil {
		return zero, key.NodePrivate{}, err
	}
	if err := rejectSymlink(s.dir); err != nil {
		return zero, key.NodePrivate{}, err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return zero, key.NodePrivate{}, Errorf("无法创建身份目录：%v", err)
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return zero, key.NodePrivate{}, Errorf("无法设置身份目录权限：%v", err)
	}

	lock, err := platform.LockFile(ctx, filepath.Join(s.dir, name+".lock"), identityLockWait)
	if err != nil {
		return zero, key.NodePrivate{}, Errorf("客户端身份正在由另一进程初始化，请稍后重试（%v）", err)
	}
	defer lock.Unlock()

	path := s.filePath(name)
	if err := rejectSymlink(path); err != nil {
		return zero, key.NodePrivate{}, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// No --force equivalent: a missing file is the only time we generate.
		generated := s.newKey()
		raw, err := json.MarshalIndent(generated, "", "\t")
		if err != nil {
			return zero, key.NodePrivate{}, Errorf("无法编码客户端身份：%v", err)
		}
		if err := writeFileExclusive(path, raw); err != nil {
			return zero, key.NodePrivate{}, Errorf("无法保存客户端身份：%v", err)
		}
	} else if err != nil {
		return zero, key.NodePrivate{}, Errorf("无法读取客户端身份：%v", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		return zero, key.NodePrivate{}, Errorf("无法读取客户端身份：%v", err)
	}
	if !info.Mode().IsRegular() {
		return zero, key.NodePrivate{}, Error("客户端身份必须是当前用户拥有的普通文件")
	}
	if info.Size() > maxIdentityFileSize {
		return zero, key.NodePrivate{}, Error("客户端身份文件异常，请检查身份文件；不会自动更换身份")
	}
	if !platform.OwnedByCurrentUser(info) {
		return zero, key.NodePrivate{}, Error("客户端身份必须是当前用户拥有的普通文件")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return zero, key.NodePrivate{}, Errorf("无法设置身份文件权限：%v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return zero, key.NodePrivate{}, Errorf("无法读取客户端身份：%v", err)
	}
	var priv tailcat.PrivateKey
	if err := json.Unmarshal(raw, &priv); err != nil {
		return zero, key.NodePrivate{}, Error("已有客户端身份无法读取，请检查身份文件；不会自动更换身份")
	}
	pub := priv.Private.Public().String()
	if !publicKeyPattern.MatchString(pub) {
		return zero, key.NodePrivate{}, Error("已有客户端身份无法读取，请检查身份文件；不会自动更换身份")
	}
	return Identity{File: path, PublicKey: pub}, priv.Private, nil
}

// writeFileExclusive writes data with owner-only permissions, refusing to
// replace an existing file.
func writeFileExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
