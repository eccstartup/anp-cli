// Package attachment implements ANP P7 attachments and object transfer: a
// signed client over the attachment.* control-plane JSON-RPC methods, with the
// object bytes moved over a separate HTTPS data plane (PUT upload / GET
// download). The message plane (direct.send / group.send) carries only the
// attachment_manifest.
package attachment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/transport"
)

const (
	Profile             = "anp.attachment.v1"
	SecurityProfile     = "transport-protected"
	ManifestContentType = "application/anp-attachment-manifest+json"

	// Object encryption modes (P7). The transport-protected path only supports
	// "none" in v1; object-level E2EE ("object-e2ee") is out of scope here.
	EncryptionNone = "none"
)

// Service wraps the backend attachment.* methods with the active identity's
// signed client.
type Service struct {
	Config *config.Resolved
	Active *identity.Identity
	Client *transport.Client
}

func NewService(resolved *config.Resolved, active *identity.Identity, client *transport.Client) *Service {
	return &Service{Config: resolved, Active: active, Client: client}
}

// RegisterDocument registers the local DID document with the backend so it can
// verify this identity's HTTP signature on subsequent attachment.* calls.
func (s *Service) RegisterDocument(ctx context.Context) error {
	if _, err := s.Client.CallRaw(ctx, "did.register_document", map[string]any{
		"did": s.Active.DID, "did_document": s.Active.DIDDocument,
	}); err != nil {
		return fmt.Errorf("register did document: %w", err)
	}
	return nil
}

// CreateSlotRequest is the attachment.create_slot request body.
type CreateSlotRequest struct {
	AttachmentID   string
	ExpectedSize   int64 // upload object byte size
	MimeType       string
	Filename       string
	ExpectedDigest map[string]any // optional {alg, value_b64u}
	IntendedTarget map[string]any // optional {kind, did}
}

// CreateSlot invokes attachment.create_slot.
func (s *Service) CreateSlot(ctx context.Context, req CreateSlotRequest) (map[string]any, error) {
	if strings.TrimSpace(req.AttachmentID) == "" {
		return nil, fmt.Errorf("attachment_id is required")
	}
	body := map[string]any{
		"attachment_id":                     req.AttachmentID,
		"intended_message_security_profile": SecurityProfile,
		"object_encryption_mode":            EncryptionNone,
	}
	if req.ExpectedSize > 0 {
		body["expected_size"] = fmt.Sprintf("%d", req.ExpectedSize)
	}
	if req.MimeType != "" {
		body["mime_type"] = req.MimeType
	}
	if req.Filename != "" {
		body["filename"] = req.Filename
	}
	if req.ExpectedDigest != nil {
		body["expected_digest"] = req.ExpectedDigest
	}
	if req.IntendedTarget != nil {
		body["intended_target"] = req.IntendedTarget
	}
	return s.call(ctx, "attachment.create_slot", body)
}

// CommitRequest is the attachment.commit_object request body.
type CommitRequest struct {
	AttachmentID string
	SlotID       string
	CommitToken  string
	Size         int64
	DigestValue  string // base64url (no padding) sha-256 of the uploaded bytes
}

// CommitObject invokes attachment.commit_object.
func (s *Service) CommitObject(ctx context.Context, req CommitRequest) (map[string]any, error) {
	if strings.TrimSpace(req.AttachmentID) == "" || strings.TrimSpace(req.SlotID) == "" || strings.TrimSpace(req.CommitToken) == "" {
		return nil, fmt.Errorf("attachment_id, slot_id, and commit_token are required")
	}
	if req.DigestValue == "" {
		return nil, fmt.Errorf("digest is required")
	}
	body := map[string]any{
		"attachment_id":          req.AttachmentID,
		"slot_id":                req.SlotID,
		"commit_token":           req.CommitToken,
		"size":                   fmt.Sprintf("%d", req.Size),
		"digest":                 map[string]any{"alg": "sha-256", "value_b64u": req.DigestValue},
		"object_encryption_mode": EncryptionNone,
	}
	return s.call(ctx, "attachment.commit_object", body)
}

// AbortObject invokes attachment.abort_object.
func (s *Service) AbortObject(ctx context.Context, attachmentID, slotID string) (map[string]any, error) {
	if strings.TrimSpace(attachmentID) == "" || strings.TrimSpace(slotID) == "" {
		return nil, fmt.Errorf("attachment_id and slot_id are required")
	}
	return s.call(ctx, "attachment.abort_object", map[string]any{
		"attachment_id": attachmentID,
		"slot_id":       slotID,
	})
}

// DownloadTicketRequest is the attachment.get_download_ticket request body.
type DownloadTicketRequest struct {
	AttachmentID           string
	ObjectURI              string
	MessageID              string
	MessageSecurityProfile string
	MessageTargetDID       string // direct message context (the downloader)
	GroupDID               string // group message context
}

// GetDownloadTicket invokes attachment.get_download_ticket. requester_did must
// equal meta.sender_did, so it is always the active identity DID.
func (s *Service) GetDownloadTicket(ctx context.Context, req DownloadTicketRequest) (map[string]any, error) {
	if strings.TrimSpace(req.AttachmentID) == "" || strings.TrimSpace(req.ObjectURI) == "" {
		return nil, fmt.Errorf("attachment_id and object_uri are required")
	}
	if strings.TrimSpace(req.MessageID) == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	body := map[string]any{
		"attachment_id":            req.AttachmentID,
		"object_uri":               req.ObjectURI,
		"requester_did":            s.Active.DID,
		"message_security_profile": req.MessageSecurityProfile,
		"message_id":               req.MessageID,
	}
	if strings.TrimSpace(req.GroupDID) != "" {
		body["group_did"] = req.GroupDID
	} else {
		body["message_target_did"] = req.MessageTargetDID
	}
	return s.call(ctx, "attachment.get_download_ticket", body)
}

// meta builds the common P7 meta object. The target is always the attachment
// service (target.kind = "service").
func (s *Service) meta(operationID string) map[string]any {
	return map[string]any{
		"profile":          Profile,
		"security_profile": SecurityProfile,
		"sender_did":       s.Active.DID,
		"target":           map[string]any{"kind": "service", "did": s.serviceDID()},
		"operation_id":     operationID,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *Service) call(ctx context.Context, method string, body map[string]any) (map[string]any, error) {
	meta := s.meta(newOperationID())
	return s.Client.CallRaw(ctx, method, map[string]any{"meta": meta, "body": body})
}

// serviceDID returns the attachment object service DID. The attachment control
// plane is resolved from the same message service DID used by other profiles.
func (s *Service) serviceDID() string {
	if strings.TrimSpace(s.Config.ServiceDID) != "" {
		return strings.TrimSpace(s.Config.ServiceDID)
	}
	return ""
}

// SHA256B64U computes the base64url (no padding) SHA-256 digest of data.
func SHA256B64U(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Upload puts object bytes to the upload URI over HTTPS (data plane). The
// upload is a raw octet stream, never an ANP JSON-RPC message.
func Upload(ctx context.Context, uploadURI string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURI, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(data))
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload object: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Download fetches object bytes from the object URI with a bearer download
// ticket and verifies length and SHA-256 digest before returning.
func Download(ctx context.Context, objectURI, ticket string, expectedSize int64, expectedDigestB64U string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURI, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ticket)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("download object: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download object: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, expectedSize+1))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("object size mismatch: got %d bytes, want %d", len(data), expectedSize)
	}
	if SHA256B64U(data) != expectedDigestB64U {
		return nil, fmt.Errorf("object digest mismatch")
	}
	return data, nil
}

func newOperationID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("op_%d", time.Now().UnixNano())
	}
	return "op_" + hex.EncodeToString(buffer)
}

// NewAttachmentID generates a message-local attachment identifier.
func NewAttachmentID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("att_%d", time.Now().UnixNano())
	}
	return "att_" + hex.EncodeToString(buffer)
}
