package credstore

import (
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
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

func TestServiceNameIsTheSwiftOne(t *testing.T) {
	if Service != "cc.coauto.tailcat-manager.connections" {
		t.Fatalf("Service = %q; changing it orphans every saved credential", Service)
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
