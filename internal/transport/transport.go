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
	"net/http"
	"net/url"
	"strings"

	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

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
	ID      int            `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int             `json:"id"`
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
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Signer: signer, HTTP: &http.Client{}}
}

// Call invokes a JSON-RPC method and decodes the result into out. When signer
// is nil the request is unsigned (anonymous resolution).
func (c *Client) Call(ctx context.Context, method string, params map[string]any, out any) error {
	endpoint := c.BaseURL + "/rpc"
	payload := rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1}
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
	respBody, err := io.ReadAll(resp.Body)
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

// ResolveDIDDocument resolves a did:wba or did:web document over HTTPS.
func ResolveDIDDocument(ctx context.Context, did string) (map[string]any, error) {
	resourceURL, err := documentURL(did)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", did, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resolve %s: HTTP %d", did, resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode did document for %s: %w", did, err)
	}
	return doc, nil
}

func documentURL(did string) (string, error) {
	if !strings.HasPrefix(did, "did:wba:") && !strings.HasPrefix(did, "did:web:") {
		return "", fmt.Errorf("unsupported DID method %q (only did:wba and did:web)", did)
	}
	parts := strings.SplitN(did, ":", 4)
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid did %q", did)
	}
	domain, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid did domain: %w", err)
	}
	pathPart := parts[3]
	if pathPart != "" {
		pathPart = "/" + strings.ReplaceAll(pathPart, ":", "/")
	}
	if strings.HasPrefix(did, "did:wba:") {
		return "https://" + domain + pathPart + "/did.json", nil
	}
	return "https://" + domain + pathPart + "/did.json", nil
}

// FetchJSON fetches a JSON document from an arbitrary URL (discovery crawl).
func FetchJSON(ctx context.Context, resourceURL string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", resourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", resourceURL, resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
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
