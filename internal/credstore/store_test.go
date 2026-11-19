package credstore

import (
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/google/uuid"
)

// The Swift build keyed credentials by Swift's UUID.uuidString, which is upper
// case. Reading an existing entry only works if we produce the same account.
func TestAccountKeyMatchesSwiftUUIDString(t *testing.T) {
	id := uuid.MustParse("6f1c0f1a-3b7e-4c55-9f4b-2a8f0d5c1234")
	const want = "6F1C0F1A-3B7E-4C55-9F4B-2A8F0D5C1234"
	if got := AccountKey(id); got != want {
		t.Fatalf("AccountKey() = %q, want %q", got, want)
	}
	if strings.ToLower(AccountKey(id)) != id.String() {
		t.Fatalf("AccountKey() is not the same UUID in another case: %q", AccountKey(id))
	}
}

func TestServiceNameIsTheCurrentOne(t *testing.T) {
	if Service != "cc.nicoi.tailcat-manager.connections" {
		t.Fatalf("Service = %q; changing it orphans every saved credential", Service)
	}
}

// The Swift build and 0.1.0 wrote under the old name. Reading it keeps those
// addresses without the user typing them again (see TestServicePrefixIsTheBundleIdentifier).
func TestLegacyServiceNameIsTheSwiftOne(t *testing.T) {
	if legacyService != "cc.coauto.tailcat-manager.connections" {
		t.Fatalf("legacyService = %q, want the name the Swift build used", legacyService)
	}
}

// fakeSecrets is a credential store in memory, one map per service name.
type fakeSecrets struct{ data map[string]string }

func (f *fakeSecrets) Get(key string) ([]byte, error) {
	if v, ok := f.data[key]; ok {
		return []byte(v), nil
	}
	return nil, mygo.ErrSecretNotFound
}

func (f *fakeSecrets) Set(key string, value []byte) error {
	f.data[key] = string(value)
	return nil
}

func (f *fakeSecrets) Delete(key string) error {
	if _, ok := f.data[key]; !ok {
		return mygo.ErrSecretNotFound
	}
	delete(f.data, key)
	return nil
}

func fakeStore(t *testing.T) (*Store, *fakeSecrets, *fakeSecrets) {
	t.Helper()
	now := &fakeSecrets{data: map[string]string{}}
	old := &fakeSecrets{data: map[string]string{}}
	return &Store{secrets: now, legacy: old}, now, old
}

func TestReadFallsBackToTheLegacyServiceName(t *testing.T) {
	store, now, old := fakeStore(t)
	id := uuid.New()
	old.data[AccountKey(id)] = "tc" + strings.Repeat("L", 24)

	address, ok, err := store.Read(id)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok || address != "tc"+strings.Repeat("L", 24) {
		t.Fatalf("Read() = %q, %v; want the legacy address", address, ok)
	}
	if _, ok := now.data[AccountKey(id)]; ok {
		t.Fatal("Read() wrote the legacy address back to the current service")
	}
}

func TestWriteGoesToTheCurrentServiceNameOnly(t *testing.T) {
	store, now, old := fakeStore(t)
	id := uuid.New()
	if err := store.Write("tc"+strings.Repeat("N", 24), id); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if now.data[AccountKey(id)] != "tc"+strings.Repeat("N", 24) {
		t.Fatalf("Write() did not reach the current service: %v", now.data)
	}
	if len(old.data) != 0 {
		t.Fatalf("Write() touched the legacy service: %v", old.data)
	}
}

// A device that is removed and added back must not inherit the address the
// legacy service still holds.
func TestDeleteClearsBothServiceNames(t *testing.T) {
	store, now, old := fakeStore(t)
	id := uuid.New()
	now.data[AccountKey(id)] = "tc" + strings.Repeat("N", 24)
	old.data[AccountKey(id)] = "tc" + strings.Repeat("L", 24)

	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok := now.data[AccountKey(id)]; ok {
		t.Fatal("Delete() left the current service's entry")
	}
	if _, ok := old.data[AccountKey(id)]; ok {
		t.Fatal("Delete() left the legacy service's entry")
	}
}

// Write must reject anything the credential stores cannot hold before it
// touches the system store, so an invalid address never half-writes.
func TestWriteRejectsUnstorableAddress(t *testing.T) {
	store := New()
	cases := []struct {
		name    string
		address string
	}{
		{"implausible", "hello"},
		{"empty", ""},
		{"over the store limit", "tc" + strings.Repeat("A", core.MaxStorableCredential)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Write(tc.address, uuid.New()); err == nil {
				t.Fatalf("Write(%d bytes) = nil, want an error", len(tc.address))
			}
		})
	}
}

var _ core.CredentialStore = (*Store)(nil)
