package group

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/testutil/mockbackend"
	"github.com/eccstartup/anp-cli/internal/transport"
)

func newAgent(t *testing.T, mock *mockbackend.Server, baseURL, name string) (*identity.Identity, *Service) {
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
	signer, err := identity.SignerFor(saved)
	if err != nil {
		t.Fatalf("SignerFor %s: %v", name, err)
	}
	client := transport.NewClient(baseURL, signer)
	if _, err := client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": saved.DID, "did_document": saved.DIDDocument,
	}); err != nil {
		t.Fatalf("register document %s: %v", name, err)
	}
	svc := NewService(&config.Resolved{
		Backend:    baseURL,
		Paths:      config.PathsFor(filepath.Join(t.TempDir(), "ws")),
		ServiceDID: "did:wba:example.com:service:anp",
	}, saved, client)
	return saved, svc
}

func TestGroupLifecycle(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	alice, aliceSvc := newAgent(t, mock, baseURL, "alice")
	bob, bobSvc := newAgent(t, mock, baseURL, "bob")

	// Create a group (alice is owner).
	created, err := aliceSvc.Create(context.Background(), CreateOptions{
		Name:   "dev room",
		Policy: map[string]any{"admission_mode": "open-join", "permissions": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	groupDID, _ := created["group_did"].(string)
	if groupDID == "" {
		t.Fatalf("Create returned no group_did: %v", created)
	}
	if creator, _ := created["creator_did"].(string); creator != alice.DID {
		t.Fatalf("creator_did = %q, want %q", creator, alice.DID)
	}

	// Bob joins the open group.
	joined, err := bobSvc.Join(context.Background(), groupDID)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if status, _ := joined["membership_status"].(string); status != "active" {
		t.Fatalf("join membership_status = %q", status)
	}

	// Alice adds a member by DID.
	if _, err := aliceSvc.Add(context.Background(), groupDID, bob.DID, "", "admin"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Info includes the member list and policy.
	info, err := aliceSvc.GetInfo(context.Background(), groupDID, true, true)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if _, ok := info["group_policy"]; !ok {
		t.Fatalf("GetInfo missing group_policy: %v", info)
	}
	memberList, _ := info["member_list"].([]any)
	if len(memberList) != 2 {
		t.Fatalf("member_list len = %d, want 2", len(memberList))
	}

	// Group send stores a transport-protected plaintext message.
	sent, err := aliceSvc.Send(context.Background(), groupDID, SendOptions{Text: "hello group"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if accepted, _ := sent["accepted"].(bool); !accepted {
		t.Fatalf("group send not accepted: %v", sent)
	}
	stored := mock.Messages()
	if len(stored) != 1 {
		t.Fatalf("backend stored %d messages, want 1", len(stored))
	}

	// Update profile and policy.
	if _, err := aliceSvc.UpdateProfile(context.Background(), groupDID, map[string]any{"description": "new"}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if _, err := aliceSvc.UpdatePolicy(context.Background(), groupDID, map[string]any{"admission_mode": "admin-add"}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	// Remove bob, then bob leaves (no-op removal).
	if _, err := aliceSvc.Remove(context.Background(), groupDID, bob.DID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := bobSvc.Leave(context.Background(), groupDID); err != nil {
		t.Fatalf("Leave: %v", err)
	}
}

func TestGroupCreateRequiresPolicy(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	_, aliceSvc := newAgent(t, mock, baseURL, "alice")
	if _, err := aliceSvc.Create(context.Background(), CreateOptions{Name: "no policy"}); err == nil {
		t.Fatalf("expected error for missing policy")
	}
}
