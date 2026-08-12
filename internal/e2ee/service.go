// Package e2ee wraps the ANP SDK direct E2EE reference client: prekey bundle
// publishing, X3DH session establishment, ratchet encryption/decryption, and
// local session stores.
package e2ee

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/transport"
	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
	directe2ee "github.com/agent-network-protocol/anp/golang/direct_e2ee"
)

// Re-exported wire content types so callers do not need the SDK import.
const (
	ContentTypeDirectInit   = directe2ee.ContentTypeDirectInit
	ContentTypeDirectCipher = directe2ee.ContentTypeDirectCipher
)

// Service is the E2EE facade for the active identity.
type Service struct {
	client   *directe2ee.MessageServiceDirectE2eeClient
	active   *identity.Identity
	resolved *config.Resolved
	rpc      *transport.Client
	root     string
}

// NewService assembles the E2EE client with file-backed stores under
// <workspace>/e2ee/.
func NewService(ctx context.Context, resolved *config.Resolved, active *identity.Identity, rpc *transport.Client) (*Service, error) {
	if rpc == nil {
		return nil, fmt.Errorf("e2ee requires a backend")
	}
	root := filepath.Join(resolved.Paths.Root, "e2ee")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	key1, err := loadPrivateKey(active.Keys.Key1Private, "key-1 (Ed25519)")
	if err != nil {
		return nil, err
	}
	key3, err := loadPrivateKey(active.Keys.Key3Private, "key-3 (E2EE agreement)")
	if err != nil {
		return nil, err
	}
	sessionStore, err := directe2ee.NewFileSessionStore(root)
	if err != nil {
		return nil, err
	}
	signedStore, err := directe2ee.NewFileSignedPrekeyStore(root)
	if err != nil {
		return nil, err
	}
	oneTimeStore, err := directe2ee.NewFileOneTimePrekeyStore(root)
	if err != nil {
		return nil, err
	}
	// The prekey bundle object proof is Ed25519-only in the SDK, so key-1
	// (assertionMethod-authorized) signs the bundle; key-3 handles X3DH.
	client, err := directe2ee.NewMessageServiceDirectE2eeClient(
		active.DID,
		key1,
		active.DID+"#"+anpauth.VMKeyAuth,
		key3,
		active.DID+"#"+anpauth.VMKeyE2EEAgreement,
		rpcAdapter(rpc),
		didResolver(resolved, active, rpc),
		sessionStore,
		signedStore,
		oneTimeStore,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize e2ee: %w", err)
	}
	return &Service{client: client, active: active, resolved: resolved, rpc: rpc, root: root}, nil
}

// SessionStatus reports whether a local session exists with the peer.
func (s *Service) SessionStatus(peer string) (exists bool, sessionID string, err error) {
	sessionStore, err := directe2ee.NewFileSessionStore(s.root)
	if err != nil {
		return false, "", err
	}
	session, found, err := sessionStore.FindByPeerDID(peer)
	if err != nil {
		return false, "", err
	}
	if found {
		return true, session.SessionID, nil
	}
	return false, "", nil
}

// PublishPrekeyBundle generates (if needed) and publishes the local signed
// prekey bundle plus fresh one-time prekeys to the backend.
func (s *Service) PublishPrekeyBundle() (map[string]any, error) {
	result, err := s.client.PublishPrekeyBundle()
	if err != nil {
		return nil, fmt.Errorf("publish prekey bundle: %w", err)
	}
	return result, nil
}

// EnsureReady publishes a prekey bundle if none is stored yet.
func (s *Service) EnsureReady() (map[string]any, error) {
	return s.PublishPrekeyBundle()
}

// Send encrypts text to peerDID and delivers the ciphertext via the backend.
func (s *Service) Send(ctx context.Context, peerDID string, text string, messageID string) (map[string]any, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("message_id is required for e2ee send")
	}
	result, err := s.client.SendText(ctx, peerDID, text, messageID, messageID)
	if err != nil {
		return nil, fmt.Errorf("e2ee send: %w", err)
	}
	return result, nil
}

// ProcessInbound decrypts an inbound wire message (direct_init or direct_cipher).
func (s *Service) ProcessInbound(ctx context.Context, wire map[string]any) (map[string]any, error) {
	return s.client.ProcessIncoming(ctx, wire)
}

// IsSecureWire reports whether an inbound wire message carries an encrypted
// E2EE payload.
func IsSecureWire(wire map[string]any) bool {
	meta, _ := wire["meta"].(map[string]any)
	switch meta["content_type"] {
	case directe2ee.ContentTypeDirectInit, directe2ee.ContentTypeDirectCipher:
		return true
	}
	return false
}

// PlaintextText extracts the decrypted text from a ProcessInbound result.
func PlaintextText(result map[string]any) (string, bool) {
	plaintext, _ := result["plaintext"].(map[string]any)
	text, _ := plaintext["text"].(string)
	return text, text != ""
}

func rpcAdapter(c *transport.Client) directe2ee.RPCClient {
	return func(method string, params map[string]any) (map[string]any, error) {
		return c.CallRaw(context.Background(), method, params)
	}
}

// didResolver resolves local DIDs from the identity store first, then the
// backend's did.resolve, then direct did:wba/did:web fetching.
func didResolver(resolved *config.Resolved, active *identity.Identity, rpc *transport.Client) directe2ee.DIDResolver {
	return func(ctx context.Context, did string) (map[string]any, error) {
		if active != nil && did == active.DID {
			return active.DIDDocument, nil
		}
		if rpc != nil {
			if result, err := rpc.CallRaw(ctx, "did.resolve", map[string]any{"target": did}); err == nil {
				if doc, ok := result["did_document"].(map[string]any); ok {
					return doc, nil
				}
			}
		}
		if doc, err := transport.ResolveDIDDocument(ctx, did); err == nil {
			return doc, nil
		}
		return nil, fmt.Errorf("resolve did %s: not found locally, via backend, or over HTTPS", did)
	}
}

func loadPrivateKey(path string, label string) (anp.PrivateKeyMaterial, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return anp.PrivateKeyMaterial{}, fmt.Errorf("read %s: %w", label, err)
	}
	key, err := anp.PrivateKeyFromPEM(string(raw))
	if err != nil {
		return anp.PrivateKeyMaterial{}, fmt.Errorf("parse %s: %w", label, err)
	}
	return key, nil
}
