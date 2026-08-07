package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

func TestSigningRoundTrip(t *testing.T) {
	bundle, err := anpauth.CreateDidWBADocument("example.com", anpauth.DidDocumentOptions{PathSegments: []string{"agent", "alice"}})
	if err != nil {
		t.Fatalf("CreateDidWBADocument: %v", err)
	}
	privateKey, err := anp.PrivateKeyFromPEM(bundle.Keys[anpauth.VMKeyAuth].PrivateKeyPEM)
	if err != nil {
		t.Fatalf("PrivateKeyFromPEM: %v", err)
	}
	signer := &Signer{DidDocument: bundle.DidDocument, PrivateKey: privateKey}
	requestURL := "http://mock.example.com/rpc"
	body := []byte(`{"jsonrpc":"2.0","method":"ping","params":{},"id":1}`)
	headers, err := signer.SignHeaders(http.MethodPost, requestURL, body)
	if err != nil {
		t.Fatalf("SignHeaders: %v", err)
	}
	if _, err := anpauth.VerifyHTTPMessageSignature(bundle.DidDocument, http.MethodPost, requestURL, headers, body); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestClientCallSigned(t *testing.T) {
	bundle, err := anpauth.CreateDidWBADocument("example.com", anpauth.DidDocumentOptions{PathSegments: []string{"agent", "alice"}})
	if err != nil {
		t.Fatalf("CreateDidWBADocument: %v", err)
	}
	privateKey, err := anp.PrivateKeyFromPEM(bundle.Keys[anpauth.VMKeyAuth].PrivateKeyPEM)
	if err != nil {
		t.Fatalf("PrivateKeyFromPEM: %v", err)
	}
	signer := &Signer{DidDocument: bundle.DidDocument, PrivateKey: privateKey}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		headers := map[string]string{}
		for key, values := range r.Header {
			headers[key] = values[0]
		}
		requestURL := "http://" + r.Host + r.URL.Path
		if _, err := anpauth.VerifyHTTPMessageSignature(bundle.DidDocument, http.MethodPost, requestURL, headers, body); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad signature"}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"ok":true},"id":1}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, signer)
	var result map[string]any
	if err := client.Call(context.Background(), "ping", map[string]any{}, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %v", result)
	}
}

func TestClientCallUnsigned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"hello":"world"},"id":1}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, nil)
	var result map[string]any
	if err := client.Call(context.Background(), "ping", map[string]any{}, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result["hello"] != "world" {
		t.Fatalf("result = %v", result)
	}
}

func TestDocumentURL(t *testing.T) {
	cases := []struct {
		did  string
		want string
	}{
		{"did:wba:example.com:user:alice:e1_x", "https://example.com/user/alice/e1_x/did.json"},
		{"did:web:example.com:agent:bot", "https://example.com/agent/bot/did.json"},
	}
	for _, tc := range cases {
		got, err := documentURL(tc.did)
		if err != nil {
			t.Fatalf("documentURL(%s): %v", tc.did, err)
		}
		if got != tc.want {
			t.Fatalf("documentURL(%s) = %q, want %q", tc.did, got, tc.want)
		}
	}
	if _, err := documentURL("did:key:z6Mk"); err == nil {
		t.Fatalf("expected error for unsupported did method")
	}
}

func TestLoadPrivateKey(t *testing.T) {
	bundle, err := anpauth.CreateDidWBADocument("example.com", anpauth.DidDocumentOptions{PathSegments: []string{"agent", "alice"}})
	if err != nil {
		t.Fatalf("CreateDidWBADocument: %v", err)
	}
	key, err := LoadPrivateKey([]byte(bundle.Keys[anpauth.VMKeyAuth].PrivateKeyPEM))
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if key.Type != anp.KeyTypeEd25519 {
		t.Fatalf("key type = %s", key.Type)
	}
}

func TestFetchJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"ocr"}`))
	}))
	defer server.Close()
	doc, err := FetchJSON(context.Background(), server.URL+"/ad.json")
	if err != nil {
		t.Fatalf("FetchJSON: %v", err)
	}
	if doc["name"] != "ocr" {
		t.Fatalf("doc = %v", doc)
	}
}
