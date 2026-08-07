package e2ee_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/e2ee"
	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/message"
	"github.com/ANPWorld/anp-cli/internal/store"
	"github.com/ANPWorld/anp-cli/internal/testutil/mockbackend"
	"github.com/ANPWorld/anp-cli/internal/transport"
)

type testAgent struct {
	identity *identity.Identity
	service  *message.Service
}

func newAgent(t *testing.T, mock *mockbackend.Server, baseURL string, name string) *testAgent {
	t.Helper()
	generated, err := identity.Generate(identity.GenerateOptions{
		Hostname: "example.com", PathSegments: []string{"agent", name}, EnableE2EE: true,
		BackendURL: baseURL, ServiceDID: "did:wba:example.com:service:anp",
	})
	if err != nil {
		t.Fatalf("Generate %s: %v", name, err)
	}
	identityStore := identity.NewStore(filepath.Join(t.TempDir(), "identities"))
	saved, err := identityStore.Save(generated, name, "")
	if err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
	mock.AddIdentity(saved.DID, saved.DIDDocument)
	db, err := store.Open(filepath.Join(t.TempDir(), "anp.db"))
	if err != nil {
		t.Fatalf("store.Open %s: %v", name, err)
	}
	t.Cleanup(func() { db.Close() })
	signer, err := identity.SignerFor(saved)
	if err != nil {
		t.Fatalf("SignerFor %s: %v", name, err)
	}
	client := transport.NewClient(baseURL, signer)
	service := message.NewService(&config.Resolved{Backend: baseURL, Paths: config.PathsFor(filepath.Join(t.TempDir(), "ws"))}, db, saved, client)
	return &testAgent{identity: saved, service: service}
}

// publishBundle registers the DID document and publishes the prekey bundle,
// mirroring `anp-cli e2ee init`.
func (a *testAgent) publishBundle(t *testing.T) {
	t.Helper()
	if _, err := a.service.Client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": a.identity.DID, "did_document": a.identity.DIDDocument,
	}); err != nil {
		t.Fatalf("register document: %v", err)
	}
	svc, err := e2ee.NewService(context.Background(), a.service.Config, a.identity, a.service.Client)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.PublishPrekeyBundle(); err != nil {
		t.Fatalf("PublishPrekeyBundle: %v", err)
	}
}

func TestSecureTwoAgentRoundTrip(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newAgent(t, mock, baseURL, "alice")
	bob := newAgent(t, mock, baseURL, "bob")

	// Bob must publish his prekey bundle before Alice can establish a session.
	bob.publishBundle(t)

	sent, err := alice.service.Send(context.Background(), message.SendOptions{
		To: bob.identity.DID, Text: "top secret", Secure: true,
	})
	if err != nil {
		t.Fatalf("alice secure send: %v", err)
	}
	if !sent.Secure || sent.MessageID == "" {
		t.Fatalf("unexpected sent message: %+v", sent)
	}

	// Bob pulls his inbox; the direct_init ciphertext is decrypted locally.
	pulled, err := bob.service.Sync(context.Background())
	if err != nil {
		t.Fatalf("bob sync: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("bob pulled %d messages, want 1", len(pulled))
	}
	if pulled[0].Text != "top secret" {
		t.Fatalf("decrypted text = %q, want %q", pulled[0].Text, "top secret")
	}
	if !pulled[0].Secure {
		t.Fatalf("message should be marked secure")
	}
	if pulled[0].SenderDID != alice.identity.DID {
		t.Fatalf("sender = %q, want %q", pulled[0].SenderDID, alice.identity.DID)
	}

	// The wire messages stored at the backend must be ciphertext, not plaintext:
	// Alice's direct_init plus Bob's automated handshake ACK.
	stored := mock.Messages()
	if len(stored) < 1 {
		t.Fatalf("backend stored %d messages, want >= 1", len(stored))
	}
	for _, message := range stored {
		if !message.Secure {
			t.Fatalf("backend message %s should be secure", message.MessageID)
		}
		if message.Meta == nil {
			t.Fatalf("backend message %s should carry the e2ee envelope", message.MessageID)
		}
	}
}

func TestSecureFollowUpMessage(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newAgent(t, mock, baseURL, "alice")
	bob := newAgent(t, mock, baseURL, "bob")
	bob.publishBundle(t)

	// First message establishes the session (direct_init).
	if _, err := alice.service.Send(context.Background(), message.SendOptions{To: bob.identity.DID, Text: "first", Secure: true}); err != nil {
		t.Fatalf("first secure send: %v", err)
	}
	if _, err := bob.service.Sync(context.Background()); err != nil {
		t.Fatalf("bob first sync: %v", err)
	}
	// Bob auto-ACKs; Alice must process it so her session leaves
	// pending-confirmation before sending a follow-up.
	if _, err := alice.service.Sync(context.Background()); err != nil {
		t.Fatalf("alice ack sync: %v", err)
	}
	// Second message uses the confirmed session (direct_cipher).
	if _, err := alice.service.Send(context.Background(), message.SendOptions{To: bob.identity.DID, Text: "second", Secure: true}); err != nil {
		t.Fatalf("second secure send: %v", err)
	}
	if _, err := bob.service.Sync(context.Background()); err != nil {
		t.Fatalf("bob second sync: %v", err)
	}
	inbox, err := bob.service.Inbox(store.MessageFilter{Scope: "direct", Limit: 10})
	if err != nil {
		t.Fatalf("bob inbox: %v", err)
	}
	if len(inbox) != 2 {
		t.Fatalf("bob inbox len = %d, want 2", len(inbox))
	}
	// Newest first (ORDER BY id DESC).
	if inbox[0].Text != "second" || inbox[1].Text != "first" {
		t.Fatalf("unexpected order/text: %+v", inbox)
	}
}
