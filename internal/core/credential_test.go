package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMemoryCredentialStore(t *testing.T) {
	s := NewMemoryCredentialStore()
	id := uuid.New()
	if _, ok, err := s.Read(id); ok || err != nil {
		t.Fatalf("Read() on empty store = (%v, %v), want (not found, nil)", ok, err)
	}
	if err := s.Write(sampleAddress, id); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, ok, err := s.Read(id)
	if err != nil || !ok || got != sampleAddress {
		t.Fatalf("Read() = (%q, %v, %v)", got, ok, err)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, ok, _ := s.Read(id); ok {
		t.Fatal("entry survived Delete()")
	}
}

// countingStore records how often the underlying store is touched so the cache
// can prove it prevents repeats (macOS would ask the user every time).
type countingStore struct {
	MemoryCredentialStore
	reads  int
	writes int
}

func (c *countingStore) Read(id uuid.UUID) (string, bool, error) {
	c.reads++
	return c.MemoryCredentialStore.Read(id)
}

func (c *countingStore) Write(address string, id uuid.UUID) error {
	c.writes++
	return c.MemoryCredentialStore.Write(address, id)
}

func TestCachedCredentialStore(t *testing.T) {
	base := &countingStore{}
	cached := NewCachedCredentialStore(base)
	id := uuid.New()
	if err := base.Write(sampleAddress, id); err != nil {
		t.Fatal(err)
	}
	base.reads, base.writes = 0, 0

	for i := 0; i < 3; i++ {
		if got, ok, err := cached.Read(id); err != nil || !ok || got != sampleAddress {
			t.Fatalf("Read() = (%q, %v, %v)", got, ok, err)
		}
	}
	if base.reads != 1 {
		t.Fatalf("underlying store read %d times, want 1", base.reads)
	}

	// A miss is cached too: a device without a credential must not make the
	// store prompt on every redraw.
	missing := uuid.New()
	for i := 0; i < 3; i++ {
		if _, ok, _ := cached.Read(missing); ok {
			t.Fatal("unexpected hit for a missing credential")
		}
	}
	if base.reads != 2 {
		t.Fatalf("underlying store read %d times, want 2", base.reads)
	}

	if err := cached.Write("tc"+strings.Repeat("B", 24), id); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if base.writes != 1 {
		t.Fatalf("underlying writes = %d, want 1", base.writes)
	}
	if got, _, _ := cached.Read(id); got != "tc"+strings.Repeat("B", 24) {
		t.Fatalf("cache not updated by Write: %q", got)
	}
	if err := cached.Delete(id); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, ok, _ := cached.Read(id); ok {
		t.Fatal("cache still holds a deleted credential")
	}
}

func TestCheckStorable(t *testing.T) {
	cases := []struct {
		name      string
		address   string
		wantErr   bool
		wantLimit bool
	}{
		{"typical", sampleAddress, false, false},
		{"exactly the limit", "tc" + strings.Repeat("A", MaxStorableCredential-2), false, false},
		{"one byte over the limit", "tc" + strings.Repeat("A", MaxStorableCredential-1), true, true},
		{"implausible", "not-a-tailcat-address", true, false},
		{"empty", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckStorable(tc.address)
			if tc.wantErr && err == nil {
				t.Fatalf("CheckStorable(%d bytes) = nil, want an error", len(tc.address))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("CheckStorable(%d bytes) = %v, want nil", len(tc.address), err)
			}
			if tc.wantLimit && !errors.Is(err, ErrCredentialTooLong) {
				t.Fatalf("error should be ErrCredentialTooLong, got %v", err)
			}
		})
	}
}
