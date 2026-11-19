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

// Service is the credential-store service name, the bundle identifier plus
// what the addresses are, so that nothing else in the app's identifier reads
// them by accident.
const Service = "cc.nicoi.tailcat-manager.connections"

// legacyService is the service name of the Swift build and of 0.1.0. Those
// addresses are still read, so an upgraded app does not ask for them again;
// everything written from here on goes to Service.
const legacyService = "cc.coauto.tailcat-manager.connections"

// secretsModule is the part of mygo.SecretsModule this package uses, so that
// a test can stand in for the system store.
type secretsModule interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// Store implements core.CredentialStore on top of mygo.Secrets.
type Store struct {
	secrets secretsModule
	legacy  secretsModule
}

// New returns a store backed by the system credential store.
func New() *Store {
	return &Store{
		secrets: mygo.Secrets.Service(Service),
		legacy:  mygo.Secrets.Service(legacyService),
	}
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

// Read returns the stored address, falling back to the service name the Swift
// build and 0.1.0 used.
func (s *Store) Read(id uuid.UUID) (string, bool, error) {
	sources := []secretsModule{s.secrets, s.legacy}
	var failure error
	for _, source := range sources {
		if source == nil {
			continue
		}
		raw, err := source.Get(AccountKey(id))
		if errors.Is(err, mygo.ErrSecretNotFound) {
			continue
		}
		if err != nil {
			failure = core.Errorf("无法从系统凭据库读取连接地址：%v", err)
			continue
		}
		return string(raw), true, nil
	}
	return "", false, failure
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

// Delete removes an address from both service names, so a device that is
// removed and added back does not inherit the address the old one held; a
// missing entry is not an error.
func (s *Store) Delete(id uuid.UUID) error {
	for _, source := range []secretsModule{s.secrets, s.legacy} {
		if source == nil {
			continue
		}
		if err := source.Delete(AccountKey(id)); err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
			return core.Errorf("无法从系统凭据库删除连接地址：%v", err)
		}
	}
	return nil
}

var _ core.CredentialStore = (*Store)(nil)
