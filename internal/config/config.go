// Package config resolves the workspace root, config.yaml, and environment
// overrides shared by every anp-cli command.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	EnvWorkspace = "ANP_WORKSPACE"
	EnvBackend   = "ANP_BACKEND"

	DefaultWorkspace = "~/.anp"
	DefaultBackend   = ""
)

type Paths struct {
	Root          string `json:"root"`
	ConfigFile    string `json:"config_file"`
	IdentityDir   string `json:"identity_dir"`
	DatabaseFile  string `json:"database_file"`
	DiscoveredDir string `json:"discovered_dir"`
}

type File struct {
	Backend    string `yaml:"backend,omitempty" json:"backend,omitempty"`
	DidDomain  string `yaml:"did_domain,omitempty" json:"did_domain,omitempty"`
	ServiceDID string `yaml:"service_did,omitempty" json:"service_did,omitempty"`
	Identity   string `yaml:"identity,omitempty" json:"identity,omitempty"`
}

type ValueSource struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

type Resolved struct {
	Paths          Paths                  `json:"paths"`
	ConfigFile     string                 `json:"config_file"`
	ConfigExists   bool                   `json:"config_exists"`
	ConfigError    string                 `json:"config_error,omitempty"`
	File           File                   `json:"file"`
	Backend        string                 `json:"backend"`
	DidDomain      string                 `json:"did_domain"`
	ServiceDID     string                 `json:"service_did,omitempty"`
	ActiveIdentity string                 `json:"active_identity,omitempty"`
	Sources        map[string]ValueSource `json:"sources,omitempty"`
	Format         string                 `json:"format"`
}

type Overrides struct {
	Identity        string
	IdentityChanged bool
	Format          string
	FormatChanged   bool
}

// WorkspaceRoot returns the workspace root directory, honoring $ANP_WORKSPACE.
func WorkspaceRoot() (string, error) {
	if raw := strings.TrimSpace(os.Getenv(EnvWorkspace)); raw != "" {
		return expandHome(raw), nil
	}
	return expandHome(DefaultWorkspace), nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func PathsFor(root string) Paths {
	return Paths{
		Root:          root,
		ConfigFile:    filepath.Join(root, "config.yaml"),
		IdentityDir:   filepath.Join(root, "identities"),
		DatabaseFile:  filepath.Join(root, "anp.db"),
		DiscoveredDir: filepath.Join(root, "discovered"),
	}
}

func ReadFile(path string) (File, error) {
	var file File
	raw, err := os.ReadFile(path)
	if err != nil {
		return file, err
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return file, fmt.Errorf("parse config: %w", err)
	}
	return file, nil
}

// Resolve builds the effective configuration from defaults, config.yaml, and
// environment variables, in increasing order of precedence.
func Resolve(overrides Overrides) (*Resolved, error) {
	root, err := WorkspaceRoot()
	if err != nil {
		return nil, err
	}
	paths := PathsFor(root)
	resolved := &Resolved{
		Paths:      paths,
		ConfigFile: paths.ConfigFile,
		Sources:    map[string]ValueSource{},
	}
	file, err := ReadFile(paths.ConfigFile)
	if err == nil {
		resolved.ConfigExists = true
		resolved.File = file
	} else if !os.IsNotExist(err) {
		resolved.ConfigError = err.Error()
	}

	// did_domain: config file > default.
	resolved.DidDomain = strings.TrimSpace(file.DidDomain)
	resolved.Sources["did_domain"] = ValueSource{Source: "config", Value: resolved.DidDomain}

	// service_did: config file > derived from did_domain.
	resolved.ServiceDID = strings.TrimSpace(file.ServiceDID)
	if resolved.ServiceDID == "" && resolved.DidDomain != "" {
		resolved.ServiceDID = "did:wba:" + resolved.DidDomain + ":service:anp"
	}
	resolved.Sources["service_did"] = ValueSource{Source: "config", Value: resolved.ServiceDID}

	// backend: $ANP_BACKEND > config file > default.
	backend := strings.TrimSpace(os.Getenv(EnvBackend))
	source := "env"
	if backend == "" {
		backend = strings.TrimSpace(file.Backend)
		source = "config"
	}
	if backend == "" {
		backend = DefaultBackend
		source = "default"
	}
	resolved.Backend = strings.TrimRight(backend, "/")
	resolved.Sources["backend"] = ValueSource{Source: source, Value: resolved.Backend}

	// active identity: --identity > config file.
	if overrides.IdentityChanged && strings.TrimSpace(overrides.Identity) != "" {
		resolved.ActiveIdentity = strings.TrimSpace(overrides.Identity)
		resolved.Sources["active_identity"] = ValueSource{Source: "flag", Value: resolved.ActiveIdentity}
	} else if strings.TrimSpace(file.Identity) != "" {
		resolved.ActiveIdentity = strings.TrimSpace(file.Identity)
		resolved.Sources["active_identity"] = ValueSource{Source: "config", Value: resolved.ActiveIdentity}
	}

	format := strings.TrimSpace(overrides.Format)
	if format == "" {
		format = "json"
	}
	resolved.Format = format

	return resolved, nil
}

// WriteFile atomically writes the config file.
func WriteFile(path string, file File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := yaml.Marshal(file)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
