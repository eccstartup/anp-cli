package message

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/group"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/store"
	"github.com/eccstartup/anp-cli/internal/testutil/mockbackend"
	"github.com/eccstartup/anp-cli/internal/transport"
)

type testAgent struct {
	identity *identity.Identity
	service  *Service
}

func newTestAgent(t *testing.T, mock *mockbackend.Server, baseURL, name string) *testAgent {
	t.Helper()
	generated, err := identity.Generate(identity.GenerateOptions{
		Hostname: "example.com", PathSegments: []string{"agent", name}, EnableE2EE: true,
		BackendURL: baseURL, ServiceDID: "did:wba:example.com:service:anp",
	})
	if err != nil {
		t.Fatalf("identity.Generate %s: %v", name, err)
	}
	identityStore := identity.NewStore(filepath.Join(t.TempDir(), "identities"))
	saved, err := identityStore.Save(generated, name, "")
	if err != nil {
		t.Fatalf("identityStore.Save %s: %v", name, err)
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
	service := NewService(&config.Resolved{Backend: baseURL, Paths: config.PathsFor(filepath.Join(t.TempDir(), "ws"))}, db, saved, client)
	return &testAgent{identity: saved, service: service}
}

func (a *testAgent) publishBundle(t *testing.T) {
	t.Helper()
	svc, err := a.service.ensureE2EE()
	if err != nil {
		t.Fatalf("ensureE2EE: %v", err)
	}
	if _, err := svc.PublishPrekeyBundle(); err != nil {
		t.Fatalf("PublishPrekeyBundle: %v", err)
	}
}

func TestSendAndSyncInbox(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newTestAgent(t, mock, baseURL, "alice")
	bob := newTestAgent(t, mock, baseURL, "bob")
	bob.publishBundle(t)

	// Alice sends an E2EE message to Bob.
	sent, err := alice.service.Send(context.Background(), SendOptions{To: bob.identity.DID, Text: "hello bob", Secure: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.MessageID == "" {
		t.Fatalf("no message id")
	}
	if sent.Direction != "out" {
		t.Fatalf("direction = %q", sent.Direction)
	}
	if !sent.Secure {
		t.Fatalf("sent message should be marked secure")
	}

	// Inbox from local store should contain the outbound message.
	inbox, err := alice.service.Inbox(store.MessageFilter{Scope: "direct", Limit: 10})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(inbox))
	}

	// Bob syncs and decrypts Alice's message.
	pulled, err := bob.service.Sync(context.Background())
	if err != nil {
		t.Fatalf("bob Sync: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("bob pulled %d messages, want 1", len(pulled))
	}
	if pulled[0].Text != "hello bob" {
		t.Fatalf("decrypted text = %q, want %q", pulled[0].Text, "hello bob")
	}
}

func TestSendRequiresTarget(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()
	alice := newTestAgent(t, mock, baseURL, "alice")
	_, err = alice.service.Send(context.Background(), SendOptions{Text: "no target"})
	if err == nil {
		t.Fatalf("expected error for missing target")
	}
}

func TestSendPlaintextDirect(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newTestAgent(t, mock, baseURL, "alice")
	bob := newTestAgent(t, mock, baseURL, "bob")

	// Plaintext direct send (default, transport-protected base profile).
	sent, err := alice.service.Send(context.Background(), SendOptions{To: bob.identity.DID, Text: "hello plain"})
	if err != nil {
		t.Fatalf("plaintext Send: %v", err)
	}
	if sent.Secure {
		t.Fatalf("plaintext message should not be marked secure")
	}
	if sent.Direction != "out" {
		t.Fatalf("direction = %q", sent.Direction)
	}

	// Bob syncs and reads the transport-protected plaintext directly.
	pulled, err := bob.service.Sync(context.Background())
	if err != nil {
		t.Fatalf("bob Sync: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("bob pulled %d messages, want 1", len(pulled))
	}
	if pulled[0].Text != "hello plain" {
		t.Fatalf("plaintext text = %q, want %q", pulled[0].Text, "hello plain")
	}
	if pulled[0].Secure {
		t.Fatalf("inbound plaintext should not be marked secure")
	}
}

func TestSyncReceivesGroupMessage(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newTestAgent(t, mock, baseURL, "alice")
	bob := newTestAgent(t, mock, baseURL, "bob")

	// Register alice's DID document so the mock can verify her group.send
	// HTTP signature.
	if _, err := alice.service.Client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": alice.identity.DID, "did_document": alice.identity.DIDDocument,
	}); err != nil {
		t.Fatalf("register alice document: %v", err)
	}

	// Alice creates a group and sends a transport-protected group message.
	groupSvc := group.NewService(alice.service.Config, alice.identity, alice.service.Client)
	created, err := groupSvc.Create(context.Background(), group.CreateOptions{
		Name:   "team",
		Policy: map[string]any{"admission_mode": "open-join", "permissions": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("group Create: %v", err)
	}
	groupDID, _ := created["group_did"].(string)
	if groupDID == "" {
		t.Fatalf("group Create returned no group_did: %v", created)
	}
	if _, err := groupSvc.Send(context.Background(), groupDID, group.SendOptions{Text: "hello team"}); err != nil {
		t.Fatalf("group Send: %v", err)
	}

	// Bob syncs and must receive the group message (plaintext, group scope).
	pulled, err := bob.service.Sync(context.Background())
	if err != nil {
		t.Fatalf("bob Sync: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("bob pulled %d messages, want 1", len(pulled))
	}
	if pulled[0].Text != "hello team" {
		t.Fatalf("group text = %q, want %q", pulled[0].Text, "hello team")
	}
	if pulled[0].GroupDID != groupDID {
		t.Fatalf("group_did = %q, want %q", pulled[0].GroupDID, groupDID)
	}
	if pulled[0].Secure {
		t.Fatalf("group message should not be marked secure")
	}

	// The local store exposes it under scope=group.
	inbox, err := bob.service.Inbox(store.MessageFilter{Scope: "group", Limit: 10})
	if err != nil {
		t.Fatalf("bob group inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("bob group inbox len = %d, want 1", len(inbox))
	}
	if inbox[0].Text != "hello team" || inbox[0].GroupDID != groupDID {
		t.Fatalf("unexpected group inbox message: %+v", inbox[0])
	}
}

func TestSyncReceivesGroupMentions(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice := newTestAgent(t, mock, baseURL, "alice")
	bob := newTestAgent(t, mock, baseURL, "bob")

	if _, err := alice.service.Client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": alice.identity.DID, "did_document": alice.identity.DIDDocument,
	}); err != nil {
		t.Fatalf("register alice document: %v", err)
	}

	groupSvc := group.NewService(alice.service.Config, alice.identity, alice.service.Client)
	created, err := groupSvc.Create(context.Background(), group.CreateOptions{
		Name:   "team",
		Policy: map[string]any{"admission_mode": "open-join", "permissions": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("group Create: %v", err)
	}
	groupDID, _ := created["group_did"].(string)

	mentions, err := group.BuildMentions("@alice please review", []group.MentionSpec{
		{Surface: "alice", Kind: group.MentionKindHuman, DID: "did:wba:example.com:user:alice"},
	})
	if err != nil {
		t.Fatalf("BuildMentions: %v", err)
	}
	if _, err := groupSvc.Send(context.Background(), groupDID, group.SendOptions{
		Text: "@alice please review", Mentions: mentions,
	}); err != nil {
		t.Fatalf("group Send: %v", err)
	}

	pulled, err := bob.service.Sync(context.Background())
	if err != nil {
		t.Fatalf("bob Sync: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("bob pulled %d messages, want 1", len(pulled))
	}
	if len(pulled[0].Mentions) != 1 {
		t.Fatalf("mentions len = %d, want 1: %+v", len(pulled[0].Mentions), pulled[0])
	}
	mention := pulled[0].Mentions[0].(map[string]any)
	rng := mention["range"].(map[string]any)
	if rng["start"] != float64(0) || rng["end"] != float64(6) {
		t.Fatalf("mention range = %v, want 0:6", rng)
	}
}

func TestHandleRegister(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()
	alice := newTestAgent(t, mock, baseURL, "alice")
	result, err := alice.service.Client.CallRaw(context.Background(), "handle.register", map[string]any{
		"handle": "alice.example.com",
		"did":    alice.identity.DID,
		"email":  "alice@example.com",
	})
	if err != nil {
		t.Fatalf("register handle: %v", err)
	}
	if result["status"] != "registered" {
		t.Fatalf("result = %v", result)
	}
}
