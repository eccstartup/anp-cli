package message

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/store"
	"github.com/eccstartup/anp-cli/internal/testutil/mockbackend"
	"github.com/eccstartup/anp-cli/internal/transport"
)

func newTestService(t *testing.T) (*Service, *identity.Identity, *mockbackend.Server) {
	t.Helper()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	t.Cleanup(closeFn)

	generated, err := identity.Generate(identity.GenerateOptions{Hostname: "example.com", PathSegments: []string{"agent", "alice"}, EnableE2EE: true})
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	storeDir := filepath.Join(t.TempDir(), "identities")
	identityStore := identity.NewStore(storeDir)
	saved, err := identityStore.Save(generated, "alice", "")
	if err != nil {
		t.Fatalf("identityStore.Save: %v", err)
	}
	mock.AddIdentity(saved.DID, saved.DIDDocument)

	db, err := store.Open(filepath.Join(t.TempDir(), "anp.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	signer, err := identity.SignerFor(saved)
	if err != nil {
		t.Fatalf("SignerFor: %v", err)
	}
	resolved := &config.Resolved{Backend: baseURL}
	client := transport.NewClient(baseURL, signer)
	service := NewService(resolved, db, saved, client)
	return service, saved, mock
}

func TestSendAndSyncInbox(t *testing.T) {
	service, _, _ := newTestService(t)
	sent, err := service.Send(context.Background(), SendOptions{To: "did:wba:example.com:agent:bob", Text: "hello bob"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.MessageID == "" {
		t.Fatalf("no message id")
	}
	if sent.Direction != "out" {
		t.Fatalf("direction = %q", sent.Direction)
	}
	// Inbox from local store should contain the outbound message.
	inbox, err := service.Inbox(store.MessageFilter{Scope: "direct", Limit: 10})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(inbox))
	}
	// Sync should succeed against the mock backend.
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	history, err := service.History("did:wba:example.com:agent:bob", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Text != "hello bob" {
		t.Fatalf("history text = %q", history[0].Text)
	}
}

func TestSendRequiresTarget(t *testing.T) {
	service, _, _ := newTestService(t)
	_, err := service.Send(context.Background(), SendOptions{Text: "no target"})
	if err == nil {
		t.Fatalf("expected error for missing target")
	}
}

func TestHandleRegister(t *testing.T) {
	service, active, _ := newTestService(t)
	result, err := service.Client.CallRaw(context.Background(), "handle.register", map[string]any{
		"handle": "alice.agent",
		"did":    active.DID,
		"email":  "alice@example.com",
	})
	if err != nil {
		t.Fatalf("register handle: %v", err)
	}
	if result["status"] != "registered" {
		t.Fatalf("result = %v", result)
	}
}
