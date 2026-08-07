package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCreatesE1DID(t *testing.T) {
	generated, err := Generate(GenerateOptions{Hostname: "example.com", PathSegments: []string{"agent", "alice"}, EnableE2EE: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(generated.DID, "did:wba:example.com:agent:alice:e1_") {
		t.Fatalf("unexpected did %q", generated.DID)
	}
	if generated.Key1PrivatePEM == "" || generated.Key1PublicPEM == "" {
		t.Fatalf("key-1 material missing")
	}
	if generated.Key2PrivatePEM == "" || generated.Key3PrivatePEM == "" {
		t.Fatalf("e2ee keys missing when EnableE2EE=true")
	}
	if _, ok := generated.DIDDocument["id"]; !ok {
		t.Fatalf("did document missing id")
	}
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "identities"))
	generated, err := Generate(GenerateOptions{Hostname: "example.com", PathSegments: []string{"agent", "alice"}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	saved, err := store.Save(generated, "alice", "")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.DID != generated.DID {
		t.Fatalf("saved did mismatch: %s != %s", saved.DID, generated.DID)
	}
	loaded, err := store.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DID != generated.DID {
		t.Fatalf("loaded did mismatch: %s != %s", loaded.DID, generated.DID)
	}
	if loaded.CreatedAt == "" {
		t.Fatalf("loaded identity missing created_at")
	}
	if _, err := os.Stat(loaded.Keys.Key1Private); err != nil {
		t.Fatalf("key-1 private file missing: %v", err)
	}
	if err := store.SetCurrent("alice"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	current, err := store.CurrentName()
	if err != nil {
		t.Fatalf("CurrentName: %v", err)
	}
	if current != "alice" {
		t.Fatalf("current = %q, want alice", current)
	}
}

func TestStoreMultipleIdentities(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "identities"))
	for _, name := range []string{"alice", "bob"} {
		generated, err := Generate(GenerateOptions{Hostname: "example.com", PathSegments: []string{"agent", name}})
		if err != nil {
			t.Fatalf("Generate %s: %v", name, err)
		}
		if _, err := store.Save(generated, name, ""); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	items, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list len = %d, want 2", len(items))
	}
}
