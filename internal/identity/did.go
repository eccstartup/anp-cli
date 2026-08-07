package identity

import (
	"fmt"
	"net/url"
	"strings"

	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

type GenerateOptions struct {
	Hostname     string
	PathSegments []string
	EnableE2EE   bool
	BackendURL   string
	ServiceDID   string
}

// Generate creates a fresh e1-profile DID document and key bundle via the ANP
// Go SDK. PathSegments default to ["agent", <name>]; the e1 fingerprint suffix
// is appended by the SDK.
func Generate(options GenerateOptions) (*GeneratedIdentity, error) {
	hostname := strings.TrimSpace(options.Hostname)
	if hostname == "" {
		return nil, fmt.Errorf("hostname is required to generate a did:wba document; set did_domain in config or ANP_BACKEND")
	}
	segments := options.PathSegments
	if len(segments) == 0 {
		segments = []string{"agent"}
	}
	docOptions := anpauth.DidDocumentOptions{
		PathSegments: segments,
		Domain:       hostname,
		DidProfile:   anpauth.DidProfileE1,
	}
	if options.EnableE2EE {
		enabled := true
		docOptions.EnableE2EE = &enabled
	}
	if options.BackendURL != "" {
		serviceOptions := anpauth.AnpMessageServiceOptions{}
		if options.ServiceDID != "" {
			serviceOptions.ServiceDID = options.ServiceDID
		}
		docOptions.Services = []map[string]any{
			anpauth.BuildAgentMessageService("", rpcEndpoint(options.BackendURL), serviceOptions),
		}
	}
	bundle, err := anpauth.CreateDidWBADocument(hostname, docOptions)
	if err != nil {
		return nil, fmt.Errorf("generate did document: %w", err)
	}
	did, _ := bundle.DidDocument["id"].(string)
	if did == "" {
		return nil, fmt.Errorf("generated did document is missing id")
	}
	generated := &GeneratedIdentity{
		DID:         did,
		DIDDocument: bundle.DidDocument,
	}
	if key1, ok := bundle.Keys[anpauth.VMKeyAuth]; ok {
		generated.Key1PrivatePEM = key1.PrivateKeyPEM
		generated.Key1PublicPEM = key1.PublicKeyPEM
	}
	if key2, ok := bundle.Keys[anpauth.VMKeyE2EESigning]; ok {
		generated.Key2PrivatePEM = key2.PrivateKeyPEM
	}
	if key3, ok := bundle.Keys[anpauth.VMKeyE2EEAgreement]; ok {
		generated.Key3PrivatePEM = key3.PrivateKeyPEM
	}
	return generated, nil
}

func rpcEndpoint(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/rpc") {
		return base
	}
	return base + "/rpc"
}

// BackendFromDIDDocument extracts the ANP message service endpoint, if any.
func BackendFromDIDDocument(doc map[string]any) string {
	services, ok := doc["service"].([]any)
	if !ok {
		return ""
	}
	for _, entry := range services {
		service, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if service["type"] == anpauth.ANPMessageServiceType {
			if endpoint, ok := service["serviceEndpoint"].(string); ok {
				return endpoint
			}
		}
	}
	return ""
}

// PrivateKeyFromPEM loads key-1 (Ed25519) from its PEM bytes.
func PrivateKeyFromPEM(pemBytes []byte) (anp.PrivateKeyMaterial, error) {
	return anp.PrivateKeyFromPEM(string(pemBytes))
}

// DIDDomain returns the domain of a did:wba document id, or "" for other methods.
func DIDDomain(did string) string {
	if !strings.HasPrefix(did, "did:wba:") {
		return ""
	}
	parts := strings.SplitN(did, ":", 4)
	if len(parts) < 4 {
		return ""
	}
	if domain, err := url.PathUnescape(parts[2]); err == nil {
		return domain
	}
	return parts[2]
}
