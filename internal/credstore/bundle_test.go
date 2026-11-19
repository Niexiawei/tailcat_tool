package credstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mygo.json's identifier and the credential-store service name share one
// prefix: the service name says which app the saved addresses belong to, and a
// bundle identifier that disagrees with it makes the store's name a lie.
func TestServicePrefixIsTheBundleIdentifier(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "mygo.json"))
	if err != nil {
		t.Fatalf("mygo.json: %v", err)
	}
	var cfg struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("mygo.json: %v", err)
	}
	if cfg.Identifier == "" {
		t.Fatal("mygo.json has no identifier")
	}
	if Service != cfg.Identifier+".connections" {
		t.Fatalf("Service = %q, want %q.connections", Service, cfg.Identifier)
	}
	if !strings.HasPrefix(Service, cfg.Identifier) {
		t.Fatalf("Service %q does not start with the identifier %q", Service, cfg.Identifier)
	}
}
