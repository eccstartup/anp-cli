package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveServiceSigning(t *testing.T) {
	root := t.TempDir()
	os.Setenv(EnvWorkspace, root)
	defer os.Unsetenv(EnvWorkspace)

	path := PathsFor(root).ConfigFile
	if err := WriteFile(path, File{DidDomain: "example.com", SignAsService: true}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolved, err := Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolved.SignAsService {
		t.Fatalf("SignAsService = false, want true")
	}
	if resolved.ServiceDID != "did:wba:example.com:service:anp" {
		t.Fatalf("ServiceDID = %q", resolved.ServiceDID)
	}
	if !resolved.ServiceSigningEnabled() {
		t.Fatalf("ServiceSigningEnabled() = false, want true")
	}
}

func TestResolveDefaultNoServiceSigning(t *testing.T) {
	root := t.TempDir()
	os.Setenv(EnvWorkspace, root)
	defer os.Unsetenv(EnvWorkspace)

	resolved, err := Resolve(Overrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ServiceSigningEnabled() {
		t.Fatalf("ServiceSigningEnabled() = true, want false by default")
	}
}

func TestReadFileSignAsService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteFile(path, File{SignAsService: true, ServiceDID: "did:wba:x:service:anp"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	file, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !file.SignAsService || file.ServiceDID != "did:wba:x:service:anp" {
		t.Fatalf("file = %+v", file)
	}
}
