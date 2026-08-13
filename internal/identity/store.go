// Package identity manages local ANP DID identities: generation, file-backed
// storage, and the backend-facing id operations.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eccstartup/anp-cli/internal/fslock"
)

// Index is the on-disk identity index (identities/index.json).
type Index struct {
	Current string      `json:"current,omitempty"`
	Items   []IndexItem `json:"items"`
}

type IndexItem struct {
	Name      string `json:"name"`
	DID       string `json:"did"`
	Handle    string `json:"handle,omitempty"`
	CreatedAt string `json:"created_at"`
}

type Identity struct {
	Name        string         `json:"name"`
	DID         string         `json:"did"`
	Handle      string         `json:"handle,omitempty"`
	DIDDocument map[string]any `json:"did_document"`
	CreatedAt   string         `json:"created_at"`
	Keys        KeyPaths       `json:"keys"`
}

type KeyPaths struct {
	Key1Private string `json:"key1_private,omitempty"`
	Key1Public  string `json:"key1_public,omitempty"`
	Key2Private string `json:"key2_private,omitempty"`
	Key3Private string `json:"key3_private,omitempty"`
}

type GeneratedIdentity struct {
	DID            string
	DIDDocument    map[string]any
	Key1PrivatePEM string
	Key1PublicPEM  string
	Key2PrivatePEM string
	Key3PrivatePEM string
}

type Store struct {
	root string // identities dir
}

func NewStore(identityDir string) *Store {
	return &Store{root: identityDir}
}

func (s *Store) Root() string { return s.root }

func (s *Store) indexPath() string { return filepath.Join(s.root, "index.json") }

func (s *Store) dir(name string) string { return filepath.Join(s.root, name) }

func (s *Store) EnsureRoot() error {
	return os.MkdirAll(s.root, 0o700)
}

func (s *Store) readIndex() (Index, error) {
	var index Index
	raw, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return index, nil
		}
		return index, err
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return index, fmt.Errorf("parse identity index: %w", err)
	}
	if index.Items == nil {
		index.Items = []IndexItem{}
	}
	return index, nil
}

func (s *Store) writeIndex(index Index) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.indexPath(), raw)
}

func (s *Store) Save(generated *GeneratedIdentity, name string, handle string) (*Identity, error) {
	if err := s.EnsureRoot(); err != nil {
		return nil, err
	}
	name = sanitizeName(name)

	lock, err := s.lockIndex()
	if err != nil {
		return nil, err
	}
	defer unlockIndex(lock)

	index, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	dir := s.dir(name)
	// Refuse to overwrite an existing identity.
	if fileExists(filepath.Join(dir, "did.json")) {
		return nil, fmt.Errorf("identity %q already exists", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keys := KeyPaths{
		Key1Private: filepath.Join(dir, "key-1-private.pem"),
		Key1Public:  filepath.Join(dir, "key-1-public.pem"),
	}
	if generated.Key2PrivatePEM != "" {
		keys.Key2Private = filepath.Join(dir, "key-2-private.pem")
	}
	if generated.Key3PrivatePEM != "" {
		keys.Key3Private = filepath.Join(dir, "key-3-private.pem")
	}
	if err := writeFileMode(keys.Key1Private, []byte(generated.Key1PrivatePEM), 0o600); err != nil {
		return nil, err
	}
	if err := writeFileMode(keys.Key1Public, []byte(generated.Key1PublicPEM), 0o600); err != nil {
		return nil, err
	}
	if keys.Key2Private != "" {
		if err := writeFileMode(keys.Key2Private, []byte(generated.Key2PrivatePEM), 0o600); err != nil {
			return nil, err
		}
	}
	if keys.Key3Private != "" {
		if err := writeFileMode(keys.Key3Private, []byte(generated.Key3PrivatePEM), 0o600); err != nil {
			return nil, err
		}
	}
	if err := writeJSONAtomic(filepath.Join(dir, "did.json"), generated.DIDDocument); err != nil {
		return nil, err
	}
	identity := &Identity{
		Name:        name,
		DID:         generated.DID,
		Handle:      handle,
		DIDDocument: generated.DIDDocument,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Keys:        keys,
	}
	item := IndexItem{Name: name, DID: identity.DID, Handle: handle, CreatedAt: identity.CreatedAt}
	index.Items = append(index.Items, item)
	if index.Current == "" {
		index.Current = name
	}
	if err := s.writeIndex(index); err != nil {
		return nil, err
	}
	return identity, nil
}

func (s *Store) List() ([]IndexItem, error) {
	index, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	return index.Items, nil
}

func (s *Store) CurrentName() (string, error) {
	index, err := s.readIndex()
	if err != nil {
		return "", err
	}
	return index.Current, nil
}

func (s *Store) SetCurrent(name string) error {
	lock, err := s.lockIndex()
	if err != nil {
		return err
	}
	defer unlockIndex(lock)

	index, err := s.readIndex()
	if err != nil {
		return err
	}
	for _, item := range index.Items {
		if item.Name == name {
			index.Current = name
			return s.writeIndex(index)
		}
	}
	return fmt.Errorf("unknown identity %q", name)
}

func (s *Store) Load(name string) (*Identity, error) {
	if strings.TrimSpace(name) == "" {
		index, err := s.readIndex()
		if err != nil {
			return nil, err
		}
		name = index.Current
	}
	if name == "" {
		return nil, fmt.Errorf("no identity selected; run `anp-cli init` or `anp-cli id show` to choose one")
	}
	dir := s.dir(name)
	raw, err := os.ReadFile(filepath.Join(dir, "did.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("identity %q does not exist", name)
		}
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	did, _ := doc["id"].(string)
	index, _ := s.readIndex()
	handle := ""
	createdAt := ""
	for _, item := range index.Items {
		if item.Name == name {
			handle = item.Handle
			createdAt = item.CreatedAt
			break
		}
	}
	keys := KeyPaths{
		Key1Private: filepath.Join(dir, "key-1-private.pem"),
		Key1Public:  filepath.Join(dir, "key-1-public.pem"),
	}
	if fileExists(filepath.Join(dir, "key-2-private.pem")) {
		keys.Key2Private = filepath.Join(dir, "key-2-private.pem")
	}
	if fileExists(filepath.Join(dir, "key-3-private.pem")) {
		keys.Key3Private = filepath.Join(dir, "key-3-private.pem")
	}
	return &Identity{
		Name:        name,
		DID:         did,
		Handle:      handle,
		CreatedAt:   createdAt,
		DIDDocument: doc,
		Keys:        keys,
	}, nil
}

func (s *Store) SetHandle(name string, handle string) error {
	lock, err := s.lockIndex()
	if err != nil {
		return err
	}
	defer unlockIndex(lock)

	index, err := s.readIndex()
	if err != nil {
		return err
	}
	for i := range index.Items {
		if index.Items[i].Name == name {
			index.Items[i].Handle = handle
			return s.writeIndex(index)
		}
	}
	return fmt.Errorf("unknown identity %q", name)
}

// --- index.json file lock (prevents read-modify-write races) ---

func (s *Store) lockIndex() (*os.File, error) {
	lock, err := fslock.Acquire(s.indexPath() + ".lock")
	if err != nil {
		return nil, fmt.Errorf("acquire index lock: %w", err)
	}
	return lock, nil
}

func unlockIndex(f *os.File) {
	fslock.Release(f)
}

// RandomName generates a random agent name (e.g. "agent-a3b9f2c1").
func RandomName() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return "agent-" + hex.EncodeToString(buf[:])
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = RandomName()
	}
	// Keep only safe characters and collapse path separators so the name can
	// never escape the identity directory via path traversal.
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			b.WriteRune('_')
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name = b.String()
	// Reject names that resolve to path traversal ("..") or nothing.
	if name == "" || name == "." || name == ".." {
		name = RandomName()
	}
	return name
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeFileMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func writeJSONAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, raw)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
