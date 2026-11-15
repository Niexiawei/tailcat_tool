// Package credstore keeps connection addresses in the operating system's
// credential store: the login Keychain on macOS, the Secret Service on Linux
// and the Credential Manager on Windows.
package credstore

import (
	"errors"
	"strings"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/google/uuid"
)

// Service is the credential-store service name. It is the one the Swift build
// used, so addresses saved by that build are read without re-entry.
const Service = "cc.coauto.tailcat-manager.connections"

// Store implements core.CredentialStore on top of mygo.Secrets.
type Store struct {
	secrets mygo.SecretsModule
}

// New returns a store backed by the system credential store.
func New() *Store {
	return &Store{secrets: mygo.Secrets.Service(Service)}
}

// Available reports whether this machine has a usable credential store. Linux
// without a Secret Service falls back to memory, and the caller should say so.
func Available() bool {
	probe := "tailcat-manager-probe"
	if err := mygo.Secrets.Set(probe, []byte("ok")); err != nil {
		return false
	}
	_ = mygo.Secrets.Delete(probe)
	return true
}

// AccountKey is the credential-store account for a device. The Swift build used
// Swift's UUID.uuidString, which is upper case, so we must match it exactly.
func AccountKey(id uuid.UUID) string {
	return strings.ToUpper(id.String())
}

// Read returns the stored address.
func (s *Store) Read(id uuid.UUID) (string, bool, error) {
	raw, err := s.secrets.Get(AccountKey(id))
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, core.Errorf("无法从系统凭据库读取连接地址：%v", err)
	}
	return string(raw), true, nil
}

// Write stores an address after checking it fits the smallest store.
func (s *Store) Write(address string, id uuid.UUID) error {
	if err := core.CheckStorable(address); err != nil {
		return err
	}
	if err := s.secrets.Set(AccountKey(id), []byte(address)); err != nil {
		return core.Errorf("无法写入系统凭据库：%v", err)
	}
	return nil
}

// Delete removes an address; a missing entry is not an error.
func (s *Store) Delete(id uuid.UUID) error {
	err := s.secrets.Delete(AccountKey(id))
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		return core.Errorf("无法从系统凭据库删除连接地址：%v", err)
	}
	return nil
}

var _ core.CredentialStore = (*Store)(nil)
