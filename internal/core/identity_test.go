package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/platform"
	"github.com/tailscale/tailcat"
)

func newTestIdentityStore(t *testing.T, dir string) (*IdentityStore, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	store := NewIdentityStore(dir)
	store.newKey = func() *tailcat.PrivateKey {
		calls.Add(1)
		return tailcat.NewPrivateKey()
	}
	return store, calls
}

func TestIdentityNameValidation(t *testing.T) {
	store, _ := newTestIdentityStore(t, t.TempDir())
	valid := []string{"abc", "6f1c0f1a-3b7e-4c55-9f4b-2a8f0d5c1234-connection", "a" + string(make([]byte, 0)), "A_b-9"}
	for _, name := range valid {
		if err := store.validateName(name); err != nil {
			t.Errorf("name %q rejected: %v", name, err)
		}
	}
	invalid := []string{"", "-leading", "has space", "slash/name", "dot.name", "中文", string(make([]byte, 97))}
	for _, name := range invalid {
		if err := store.validateName(name); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

func TestIdentityFileMatchesCLIFormat(t *testing.T) {
	dir := t.TempDir()
	store, _ := newTestIdentityStore(t, dir)
	id, err := store.Identity(context.Background(), "device-connection")
	if err != nil {
		t.Fatalf("Identity() error: %v", err)
	}
	raw, err := os.ReadFile(id.File)
	if err != nil {
		t.Fatalf("reading identity file: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("identity file is not JSON: %v", err)
	}
	for _, key := range []string{"Private", "Public"} {
		if _, ok := parsed[key]; !ok {
			t.Fatalf("identity file lacks %q: %s", key, raw)
		}
	}
	if !regexp.MustCompile(`(?m)^\t"Private"`).Match(raw) {
		t.Fatalf("identity file is not tab-indented like the CLI writes it: %s", raw)
	}
	// The CLI reads this file by unmarshalling the same struct.
	var roundTrip tailcat.PrivateKey
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("the CLI could not read this file: %v", err)
	}
	if got := roundTrip.Private.Public().String(); got != id.PublicKey {
		t.Fatalf("public key %q does not belong to the stored private key %q", id.PublicKey, got)
	}
	if !regexp.MustCompile(`^nodekey:[0-9a-f]{64}$`).MatchString(id.PublicKey) {
		t.Fatalf("PublicKey = %q, want nodekey:<64 hex>", id.PublicKey)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(id.File)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("identity file mode = %o, want 600", perm)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Fatalf("identity dir mode = %o, want 700", perm)
		}
	}
}

func TestIdentityCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	store, calls := newTestIdentityStore(t, dir)
	first, err := store.Identity(context.Background(), "dev-connection")
	if err != nil {
		t.Fatalf("Identity() error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("generated %d keys for the first call, want 1", calls.Load())
	}
	info, err := os.Stat(first.File)
	if err != nil {
		t.Fatal(err)
	}
	before := info.ModTime()

	// A second store instance stands in for the next app launch.
	other, otherCalls := newTestIdentityStore(t, dir)
	second, err := other.Identity(context.Background(), "dev-connection")
	if err != nil {
		t.Fatalf("Identity() on the next launch error: %v", err)
	}
	if otherCalls.Load() != 0 {
		t.Fatalf("regenerated a saved identity (%d new keys)", otherCalls.Load())
	}
	if second.PublicKey != first.PublicKey {
		t.Fatalf("identity changed across launches: %q -> %q", first.PublicKey, second.PublicKey)
	}
	info, err = os.Stat(first.File)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(before) {
		t.Fatal("identity file was rewritten although it already existed")
	}
}

func TestIdentityConcurrentInit(t *testing.T) {
	dir := t.TempDir()
	store, calls := newTestIdentityStore(t, dir)
	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	keys := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id, err := store.Identity(context.Background(), "race-connection")
			keys[i], errs[i] = id.PublicKey, err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if keys[i] != keys[0] {
			t.Fatalf("goroutine %d saw a different identity: %q vs %q", i, keys[i], keys[0])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("generated %d keys for a single identity, want 1", got)
	}
}

func TestIdentityNeverReplacesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	store, calls := newTestIdentityStore(t, dir)
	path := filepath.Join(dir, "broken-connection.private.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Identity(context.Background(), "broken-connection"); err == nil {
		t.Fatal("Identity() accepted a corrupt identity file")
	}
	if calls.Load() != 0 {
		t.Fatalf("generated %d replacement keys; a saved identity must never be replaced", calls.Load())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Fatalf("corrupt file was overwritten: %q", raw)
	}
}

func TestIdentityRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	dir := t.TempDir()
	store, _ := newTestIdentityStore(t, dir)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked-connection.private.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := store.Identity(context.Background(), "linked-connection"); err == nil {
		t.Fatal("Identity() followed a symlinked identity file")
	}
}

func TestNodeKeyReturnsStablePrivateKey(t *testing.T) {
	dir := t.TempDir()
	store, _ := newTestIdentityStore(t, dir)
	ctx := context.Background()
	id, err := store.Identity(ctx, "probe-key")
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := store.NodeKey(ctx, "probe-key")
	if err != nil {
		t.Fatalf("NodeKey() error: %v", err)
	}
	if got := nodeKey.Public().String(); got != id.PublicKey {
		t.Fatalf("NodeKey() public %q != Identity() public %q", got, id.PublicKey)
	}
}

func TestIdentityContextTimeout(t *testing.T) {
	dir := t.TempDir()
	store, _ := newTestIdentityStore(t, dir)
	// Hold the lock outside the store to prove a stuck lock fails instead of
	// hanging forever.
	lockPath := filepath.Join(dir, "busy-connection.lock")
	release, err := holdLockForTest(lockPath)
	if err != nil {
		t.Fatalf("preparing the lock: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := store.Identity(ctx, "busy-connection"); err == nil {
		t.Fatal("Identity() ignored a held lock until the context expired")
	}
}

// holdLockForTest takes the identity lock the same way the store does, so a
// second attempt has to wait.
func holdLockForTest(path string) (func(), error) {
	lock, err := platform.LockFile(context.Background(), path, time.Second)
	if err != nil {
		return nil, err
	}
	return lock.Unlock, nil
}
