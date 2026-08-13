// Package transport implements the ANP wire client: signed HTTP JSON-RPC 2.0
// calls against a backend that relays ANP messages and identity operations.
//
// Protocol (documented in docs/protocol.md): every call is
//
//	POST {backend}/rpc
//	Content-Type: application/json
//	Signature-Input / Signature / Content-Digest   (HTTP Message Signatures)
//
// with a JSON-RPC 2.0 body. Authentication uses the active identity's
// did:wba document and Ed25519 key-1.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

// requestTimeout bounds every backend/HTTPS call so a stalled peer cannot hang
// the CLI indefinitely.
const requestTimeout = 30 * time.Second

// maxResponseBytes caps how much of a backend/DID/ad.json response we buffer in
// memory, so a misbehaving or malicious peer cannot OOM the CLI with an
// unbounded body.
const maxResponseBytes = 10 << 20 // 10 MiB

// LoadPrivateKey decodes an Ed25519 PKCS#8 private key PEM for signing.
func LoadPrivateKey(pemBytes []byte) (anp.PrivateKeyMaterial, error) {
	key, err := anp.PrivateKeyFromPEM(string(pemBytes))
	if err != nil {
		return anp.PrivateKeyMaterial{}, fmt.Errorf("parse private key: %w", err)
	}
	return key, nil
}

// Signer signs HTTP requests with ANP HTTP Message Signatures.
type Signer struct {
	DidDocument map[string]any
	PrivateKey  anp.PrivateKeyMaterial
}

func (s *Signer) SignHeaders(method string, requestURL string, body []byte) (map[string]string, error) {
	base := map[string]string{"Content-Type": "application/json"}
	headers, err := anpauth.GenerateHTTPSignatureHeaders(s.DidDocument, requestURL, method, s.PrivateKey, base, body, anpauth.HttpSignatureOptions{})
	if err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}
	return headers, nil
}

// RPCError is a JSON-RPC error returned by the backend.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return e.Message
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	ID      string         `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      string          `json:"id"`
}

// Client is a signed JSON-RPC client for the ANP protocol.
type Client struct {
	BaseURL string
	Signer  *Signer
	HTTP    *http.Client
}

func NewClient(baseURL string, signer *Signer) *Client {
	if signer == nil {
		signer = &Signer{}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Signer: signer, HTTP: &http.Client{Timeout: requestTimeout}}
}

// validateBaseURL rejects plaintext HTTP backends (except loopback for local
// development) so credentials and message bodies are not sent in the clear.
func (c *Client) validateBaseURL() error {
	if c.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid backend URL: %w", err)
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		default:
			return fmt.Errorf("refusing plaintext HTTP backend %q; use https:// (loopback is allowed for local development)", c.BaseURL)
		}
	}
	return nil
}

// Call invokes a JSON-RPC method and decodes the result into out. When signer
// is nil the request is unsigned (anonymous resolution).
func (c *Client) Call(ctx context.Context, method string, params map[string]any, out any) error {
	if err := c.validateBaseURL(); err != nil {
		return err
	}
	endpoint := c.BaseURL + "/rpc"
	payload := rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: "1"}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Signer != nil && c.Signer.DidDocument != nil && c.Signer.PrivateKey.Bytes != nil {
		sigHeaders, err := c.Signer.SignHeaders(http.MethodPost, endpoint, body)
		if err != nil {
			return err
		}
		for key, value := range sigHeaders {
			req.Header.Set(key, value)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("backend request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readLimitedBody(resp.Body)
	if err != nil {
		return fmt.Errorf("read backend response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &RPCError{Code: resp.StatusCode, Message: fmt.Sprintf("backend returned HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 500))}
	}
	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("invalid backend response: %w", err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if out == nil {
		return nil
	}
	if len(rpcResp.Result) == 0 {
		return fmt.Errorf("backend returned no result")
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode backend result: %w", err)
	}
	return nil
}

// CallRaw invokes a JSON-RPC method and returns the raw result object.
func (c *Client) CallRaw(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	var result map[string]any
	if err := c.Call(ctx, method, params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ResolveDIDDocument resolves a did:wba or did:web document over HTTPS,
// delegating to the SDK so the returned document id and key-binding are
// validated against the requested DID. The SDK walks the spec-correct
// .well-known/did.json path for did:wba and rejects a mismatched id or binding.
func ResolveDIDDocument(ctx context.Context, did string) (map[string]any, error) {
	host, err := didHost(did)
	if err != nil {
		return nil, err
	}
	if isBlockedHost(host) {
		return nil, fmt.Errorf("refusing to resolve DID against blocked host %q", host)
	}
	return anpauth.ResolveDidDocument(ctx, did, false)
}

// didHost extracts the host component from a did:wba or did:web identifier so
// it can be screened before any outbound fetch.
func didHost(did string) (string, error) {
	parts := strings.SplitN(did, ":", 4)
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid did %q", did)
	}
	host, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid did domain: %w", err)
	}
	return host, nil
}

// isBlockedHost guards against SSRF: DID resolution must not fetch from IP
// literals, loopback, private, link-local, or cloud metadata addresses.
func isBlockedHost(hostport string) bool {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	// Cloud metadata endpoints commonly use a link-local IP (already covered
	// above), but guard the well-known hostname too.
	return host == "localhost" || host == "metadata.google.internal"
}

// FetchJSON fetches a JSON document from an arbitrary URL (discovery crawl).
func FetchJSON(ctx context.Context, resourceURL string) (map[string]any, error) {
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return nil, err
	}
	// Guard against the classic SSRF target (cloud metadata link-local). Loopback
	// and private hosts stay allowed: crawl is operator-triggered and testing
	// against a local server on 127.0.0.1 is a supported workflow.
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsLinkLocalUnicast() {
		return nil, fmt.Errorf("refusing to fetch from link-local host %q", parsed.Hostname())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: requestTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", resourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", resourceURL, resp.StatusCode)
	}
	raw, err := readLimitedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", resourceURL, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode %s: %w", resourceURL, err)
	}
	return doc, nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

// readLimitedBody reads an HTTP body up to maxResponseBytes, returning an error
// if the body exceeds the cap instead of buffering it unbounded.
func readLimitedBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return raw, nil
}
