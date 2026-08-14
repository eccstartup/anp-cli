package attachment

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

func TestAttachmentUploadDownloadRoundTrip(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	_, svc := newAgent(t, mock, baseURL, "alice")
	ctx := context.Background()

	payload := []byte("hello attachment object bytes")
	digest := SHA256B64U(payload)
	attachmentID := NewAttachmentID()

	slot, err := svc.CreateSlot(ctx, CreateSlotRequest{
		AttachmentID:   attachmentID,
		ExpectedSize:   int64(len(payload)),
		MimeType:       "text/plain",
		Filename:       "hello.txt",
		ExpectedDigest: map[string]any{"alg": "sha-256", "value_b64u": digest},
	})
	if err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}
	uploadURI, _ := slot["upload_uri"].(string)
	slotID, _ := slot["slot_id"].(string)
	commitToken, _ := slot["commit_token"].(string)
	objectURI, _ := slot["object_uri"].(string)
	if uploadURI == "" || slotID == "" || commitToken == "" || objectURI == "" {
		t.Fatalf("incomplete slot: %v", slot)
	}

	if err := Upload(ctx, uploadURI, payload); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	committed, err := svc.CommitObject(ctx, CommitRequest{
		AttachmentID: attachmentID,
		SlotID:       slotID,
		CommitToken:  commitToken,
		Size:         int64(len(payload)),
		DigestValue:  digest,
	})
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if ok, _ := committed["committed"].(bool); !ok {
		t.Fatalf("commit not committed: %v", committed)
	}

	ticket, err := svc.GetDownloadTicket(ctx, DownloadTicketRequest{
		AttachmentID:           attachmentID,
		ObjectURI:              objectURI,
		MessageID:              "msg_test",
		MessageSecurityProfile: SecurityProfile,
		MessageTargetDID:       svc.Active.DID,
	})
	if err != nil {
		t.Fatalf("GetDownloadTicket: %v", err)
	}
	ticketB64U, _ := ticket["download_ticket_b64u"].(string)
	if ticketB64U == "" {
		t.Fatalf("no download ticket: %v", ticket)
	}

	downloaded, err := Download(ctx, objectURI, ticketB64U, int64(len(payload)), digest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(downloaded) != string(payload) {
		t.Fatalf("downloaded bytes mismatch")
	}
}

func TestDownloadDigestMismatch(t *testing.T) {
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	_, svc := newAgent(t, mock, baseURL, "alice")
	ctx := context.Background()
	payload := []byte("some object")
	attachmentID := NewAttachmentID()
	slot, err := svc.CreateSlot(ctx, CreateSlotRequest{AttachmentID: attachmentID, ExpectedSize: int64(len(payload))})
	if err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}
	slotID, _ := slot["slot_id"].(string)
	objectURI, _ := slot["object_uri"].(string)
	commitToken, _ := slot["commit_token"].(string)
	if err := Upload(ctx, mustString(slot, "upload_uri"), payload); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, err := svc.CommitObject(ctx, CommitRequest{
		AttachmentID: attachmentID, SlotID: slotID, CommitToken: commitToken,
		Size: int64(len(payload)), DigestValue: SHA256B64U(payload),
	}); err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	ticket, err := svc.GetDownloadTicket(ctx, DownloadTicketRequest{
		AttachmentID: attachmentID, ObjectURI: objectURI, MessageID: "m",
		MessageSecurityProfile: SecurityProfile, MessageTargetDID: svc.Active.DID,
	})
	if err != nil {
		t.Fatalf("GetDownloadTicket: %v", err)
	}
	ticketB64U, _ := ticket["download_ticket_b64u"].(string)
	// Wrong digest must fail verification.
	if _, err := Download(ctx, objectURI, ticketB64U, int64(len(payload)), SHA256B64U([]byte("wrong"))); err == nil {
		t.Fatalf("expected digest mismatch error")
	}
	// Wrong size must fail verification.
	if _, err := Download(ctx, objectURI, ticketB64U, int64(len(payload))+1, SHA256B64U(payload)); err == nil {
		t.Fatalf("expected size mismatch error")
	}
}

func mustString(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}
