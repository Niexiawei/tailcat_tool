package core

import (
	"sync"

	"github.com/google/uuid"
)

// MaxStorableCredential is the longest connection address the system
// credential stores accept. Windows Credential Manager keeps 2560 bytes, so
// the app applies that limit everywhere and says so instead of failing at the
// last platform.
const MaxStorableCredential = 2560

// ErrCredentialTooLong is returned by CheckStorable for over-long addresses.
var ErrCredentialTooLong = Error("连接地址超过 2560 字节上限，无法保存到系统凭据库")

// CheckStorable validates an address before it is written to a credential
// store: it must look like a Tailcat address and fit the smallest store.
func CheckStorable(address string) error {
	if !Plausible(address) {
		return Error("请填写以 tc 开头的完整 Tailcat 连接地址")
	}
	if len(address) > MaxStorableCredential {
		return ErrCredentialTooLong
	}
	return nil
}

// CredentialStore keeps device connection addresses out of the configuration
// file. Implementations use the system store; keys are device UUIDs.
type CredentialStore interface {
	// Read returns the address and whether one exists.
	Read(id uuid.UUID) (string, bool, error)
	Write(address string, id uuid.UUID) error
	Delete(id uuid.UUID) error
}

// MemoryCredentialStore keeps addresses for this process only. It backs the
// demo mode and the tests.
type MemoryCredentialStore struct {
	mu     sync.Mutex
	values map[uuid.UUID]string
}

// NewMemoryCredentialStore returns an empty in-memory store.
func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{values: map[uuid.UUID]string{}}
}

// Read returns the stored address.
func (m *MemoryCredentialStore) Read(id uuid.UUID) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[id]
	return value, ok, nil
}

// Write stores an address.
func (m *MemoryCredentialStore) Write(address string, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[uuid.UUID]string{}
	}
	m.values[id] = address
	return nil
}

// Delete removes an address.
func (m *MemoryCredentialStore) Delete(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, id)
	return nil
}

// cacheEntry is one cached lookup; "present" distinguishes a stored address
// from a confirmed miss, so a missing credential is not looked up on every
// redraw either.
type cacheEntry struct {
	value   string
	present bool
}

type cachedCredentialStore struct {
	base  CredentialStore
	mu    sync.Mutex
	cache map[uuid.UUID]cacheEntry
}

// NewCachedCredentialStore wraps base so each device asks the system store at
// most once per process. The cache is never persisted.
func NewCachedCredentialStore(base CredentialStore) CredentialStore {
	return &cachedCredentialStore{base: base, cache: map[uuid.UUID]cacheEntry{}}
}

func (c *cachedCredentialStore) Read(id uuid.UUID) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.cache[id]; ok {
		return entry.value, entry.present, nil
	}
	value, present, err := c.base.Read(id)
	if err != nil {
		return "", false, err
	}
	c.cache[id] = cacheEntry{value: value, present: present}
	return value, present, nil
}

func (c *cachedCredentialStore) Write(address string, id uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.base.Write(address, id); err != nil {
		return err
	}
	c.cache[id] = cacheEntry{value: address, present: true}
	return nil
}

func (c *cachedCredentialStore) Delete(id uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.base.Delete(id); err != nil {
		return err
	}
	c.cache[id] = cacheEntry{}
	return nil
}
