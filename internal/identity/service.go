package identity

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/transport"
)

type Service struct {
	Store *Store
}

func NewService(identityDir string) *Service {
	return &Service{Store: NewStore(identityDir)}
}

func (s *Service) Init(resolved *config.Resolved, name string, enableE2EE bool) (*Identity, error) {
	if err := s.Store.EnsureRoot(); err != nil {
		return nil, err
	}
	domain := strings.TrimSpace(resolved.DidDomain)
	if domain == "" {
		domain = hostnameForDID(resolved.Backend)
	}
	// Ensure the DID document always carries an ANPMessageService.serviceDid so
	// E2EE works even when did_domain is configured after init.
	serviceDID := strings.TrimSpace(resolved.ServiceDID)
	if serviceDID == "" {
		serviceDID = "did:wba:" + domain + ":service:anp"
	}
	generated, err := Generate(GenerateOptions{
		Hostname:     domain,
		PathSegments: []string{"agent", sanitizeName(name)},
		EnableE2EE:   enableE2EE,
		BackendURL:   resolved.Backend,
		ServiceDID:   serviceDID,
	})
	if err != nil {
		return nil, err
	}
	return s.Store.Save(generated, name, "")
}

func (s *Service) Active() (*Identity, error) {
	return s.Store.Load("")
}

func (s *Service) Load(name string) (*Identity, error) {
	return s.Store.Load(name)
}

func (s *Service) List() ([]IndexItem, error) {
	return s.Store.List()
}

// SignerFor loads key-1 material for the identity, used for request signing.
func SignerFor(identity *Identity) (*transport.Signer, error) {
	if identity == nil {
		return nil, fmt.Errorf("no active identity; run `anp-cli init` first")
	}
	raw, err := os.ReadFile(identity.Keys.Key1Private)
	if err != nil {
		return nil, fmt.Errorf("read identity key: %w", err)
	}
	key, err := transport.LoadPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return &transport.Signer{DidDocument: identity.DIDDocument, PrivateKey: key}, nil
}

// Resolve returns the DID document for a DID or WNS handle target.
func (s *Service) Resolve(ctx context.Context, resolved *config.Resolved, target string) (map[string]any, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("resolve target is empty")
	}
	if strings.HasPrefix(target, "did:") {
		doc, err := transport.ResolveDIDDocument(ctx, target)
		if err != nil {
			return nil, err
		}
		return doc, nil
	}
	// Handle or bare name: ask the backend.
	client, err := backendClient(resolved)
	if err != nil {
		return nil, err
	}
	result, err := client.CallRaw(ctx, "did.resolve", map[string]any{"target": target})
	if err != nil {
		return nil, err
	}
	did, _ := result["did"].(string)
	doc, _ := result["did_document"].(map[string]any)
	if doc == nil {
		return nil, fmt.Errorf("backend resolved %q but returned no did_document", target)
	}
	if did == "" {
		did, _ = doc["id"].(string)
	}
	return doc, nil
}

// RegisterHandle registers a WNS handle for the active identity via the backend.
func (s *Service) RegisterHandle(ctx context.Context, resolved *config.Resolved, active *Identity, handle string, phone string, email string, otp string) (map[string]any, error) {
	client, err := signedClient(resolved, active)
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"handle": handle,
		"did":    active.DID,
	}
	if phone != "" {
		params["phone"] = phone
	}
	if email != "" {
		params["email"] = email
	}
	if otp != "" {
		params["otp"] = otp
	}
	result, err := client.CallRaw(ctx, "handle.register", params)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetHandle(active.Name, handle); err != nil {
		return nil, err
	}
	return result, nil
}

// RecoverHandle recovers a handle binding via the backend.
func (s *Service) RecoverHandle(ctx context.Context, resolved *config.Resolved, active *Identity, handle string, phone string, email string, otp string) (map[string]any, error) {
	client, err := signedClient(resolved, active)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"handle": handle}
	if phone != "" {
		params["phone"] = phone
	}
	if email != "" {
		params["email"] = email
	}
	if otp != "" {
		params["otp"] = otp
	}
	return client.CallRaw(ctx, "handle.recover", params)
}

func backendClient(resolved *config.Resolved) (*transport.Client, error) {
	if strings.TrimSpace(resolved.Backend) == "" {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	return transport.NewClient(resolved.Backend, nil), nil
}

func signedClient(resolved *config.Resolved, active *Identity) (*transport.Client, error) {
	client, err := backendClient(resolved)
	if err != nil {
		return nil, err
	}
	signer, err := SignerFor(active)
	if err != nil {
		return nil, err
	}
	client.Signer = signer
	return client, nil
}

// hostnameForDID derives a did:wba hostname from the backend URL. IP addresses
// are not valid did:wba domains, so they fall back to "localhost".
func hostnameForDID(backend string) string {
	if backend == "" {
		return "localhost"
	}
	parsed, err := url.Parse(backend)
	if err != nil {
		return "localhost"
	}
	host := parsed.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return "localhost"
	}
	return host
}

// PublicView strips private material for output.
func PublicView(identity *Identity) map[string]any {
	if identity == nil {
		return nil
	}
	handle := identity.Handle
	return map[string]any{
		"name":         identity.Name,
		"did":          identity.DID,
		"handle":       handle,
		"did_document": identity.DIDDocument,
		"created_at":   identity.CreatedAt,
		"workspace":    filepath.Dir(identity.Keys.Key1Private),
	}
}
