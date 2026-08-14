// Package message implements direct messaging over the ANP wire protocol, with
// local SQLite persistence for inbox and history. Messages are sent either as
// transport-protected plaintext (the ANP direct base profile, default) or
// end-to-end encrypted via X3DH + double ratchet (the ANP direct_e2ee profile).
package message

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/e2ee"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/proof"
	"github.com/eccstartup/anp-cli/internal/store"
	"github.com/eccstartup/anp-cli/internal/transport"
)

// Direct base profile constants (ANP P3).
const (
	DirectBaseProfile          = "anp.direct.base.v1"
	DirectBaseSecurityProfile  = "transport-protected"
	DirectBaseContentTypePlain = "text/plain"
	// DirectBaseContentTypeAttachmentManifest is the direct base content type
	// for P7 attachment manifest messages.
	DirectBaseContentTypeAttachmentManifest = "application/anp-attachment-manifest+json"
)

// GroupBaseProfile is the ANP P4 group messaging base profile (inbound group
// messages are transport-protected plaintext, read directly without decryption).
const GroupBaseProfile = "anp.group.base.v1"

type SendOptions struct {
	To     string
	Type   string
	Text   string
	Secure bool // true sends E2EE (direct_e2ee); false sends plaintext base (default)
}

type Service struct {
	Config  *config.Resolved
	DB      *sql.DB
	Active  *identity.Identity
	Client  *transport.Client
	e2eeSvc *e2ee.Service
	e2eeMu  sync.Mutex
	e2eeErr error
}

func NewService(resolved *config.Resolved, db *sql.DB, active *identity.Identity, client *transport.Client) *Service {
	return &Service{Config: resolved, DB: db, Active: active, Client: client}
}

func (s *Service) ensureE2EE() (*e2ee.Service, error) {
	s.e2eeMu.Lock()
	defer s.e2eeMu.Unlock()
	if s.e2eeSvc != nil {
		return s.e2eeSvc, nil
	}
	// Retry a previous failure instead of caching it forever: a long-lived
	// `runtime listen` must recover once the underlying dependency is fixed.
	s.e2eeErr = nil
	s.e2eeSvc, s.e2eeErr = e2ee.NewService(context.Background(), s.Config, s.Active, s.Client)
	return s.e2eeSvc, s.e2eeErr
}

// registerDocument registers the local DID document with the backend so it can
// verify this identity's HTTP signature on subsequent calls.
func (s *Service) registerDocument() error {
	if _, err := s.Client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": s.Active.DID, "did_document": s.Active.DIDDocument,
	}); err != nil {
		return fmt.Errorf("register did document: %w", err)
	}
	return nil
}

// ensureSecureReady registers the local DID document and publishes a prekey
// bundle so peers can establish secure sessions.
func (s *Service) ensureSecureReady() error {
	svc, err := s.ensureE2EE()
	if err != nil {
		return err
	}
	if err := s.registerDocument(); err != nil {
		return err
	}
	if _, err := svc.PublishPrekeyBundle(); err != nil {
		return err
	}
	return nil
}

// Send delivers a direct message through the backend and persists an outbound
// record. By default messages are transport-protected plaintext (direct base
// profile); with options.Secure they are end-to-end encrypted (direct_e2ee).
func (s *Service) Send(ctx context.Context, options SendOptions) (*store.Message, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	if strings.TrimSpace(options.Text) == "" {
		return nil, fmt.Errorf("message text is empty")
	}
	if options.To == "" {
		return nil, fmt.Errorf("--to is required")
	}
	messageID := newMessageID()
	var (
		result map[string]any
		err    error
	)
	if options.Secure {
		if err := s.ensureSecureReady(); err != nil {
			return nil, err
		}
		result, err = s.e2eeSvc.Send(ctx, options.To, options.Text, messageID)
		if err != nil {
			return nil, err
		}
	} else {
		result, err = s.sendPlain(ctx, options, messageID)
		if err != nil {
			return nil, err
		}
	}
	sentAt, _ := result["accepted_at"].(string)
	if sentAt == "" {
		sentAt, _ = result["sent_at"].(string)
	}
	if sentAt == "" {
		sentAt = time.Now().UTC().Format(time.RFC3339)
	}
	local := store.Message{
		MessageID:    messageID,
		SenderDID:    s.Active.DID,
		RecipientDID: options.To,
		Type:         "text",
		Text:         options.Text,
		Secure:       options.Secure,
		Direction:    "out",
		Read:         true,
		SentAt:       sentAt,
	}
	if err := store.UpsertMessage(s.DB, local); err != nil {
		return nil, err
	}
	return &local, nil
}

// sendPlain delivers a transport-protected plaintext message via the standard
// direct.send method with the anp.direct.base.v1 profile and an application
// layer origin proof.
func (s *Service) sendPlain(ctx context.Context, options SendOptions, messageID string) (map[string]any, error) {
	return s.sendDirectBase(ctx, options.To, DirectBaseContentTypePlain, map[string]any{"text": options.Text}, messageID)
}

// SendAttachmentManifest delivers an attachment manifest (P7) as a direct base
// message and persists an outbound record. The body wraps the attachment
// message object in a payload layer: {payload: {attachments: [...], caption?,
// primary_attachment_id?}}.
func (s *Service) SendAttachmentManifest(ctx context.Context, to string, manifest map[string]any) (*store.Message, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	if manifest == nil {
		return nil, fmt.Errorf("attachment manifest is empty")
	}
	messageID := newMessageID()
	result, err := s.sendDirectBase(ctx, to, DirectBaseContentTypeAttachmentManifest, map[string]any{"payload": manifest}, messageID)
	if err != nil {
		return nil, err
	}
	sentAt, _ := result["accepted_at"].(string)
	if sentAt == "" {
		sentAt, _ = result["sent_at"].(string)
	}
	if sentAt == "" {
		sentAt = time.Now().UTC().Format(time.RFC3339)
	}
	caption, _ := manifest["caption"].(string)
	local := store.Message{
		MessageID:    messageID,
		SenderDID:    s.Active.DID,
		RecipientDID: to,
		Type:         "attachment",
		Text:         caption,
		Direction:    "out",
		Read:         true,
		SentAt:       sentAt,
	}
	if err := store.UpsertMessage(s.DB, local); err != nil {
		return nil, err
	}
	return &local, nil
}

// sendDirectBase delivers a direct base envelope with a caller-supplied
// content type and body, resolving the target and attaching an origin proof.
func (s *Service) sendDirectBase(ctx context.Context, to, contentType string, body map[string]any, messageID string) (map[string]any, error) {
	if err := s.registerDocument(); err != nil {
		return nil, err
	}
	targetDID := to
	if !strings.HasPrefix(targetDID, "did:") {
		resolved, err := s.Client.CallRaw(ctx, "did.resolve", map[string]any{"target": to})
		if err != nil {
			return nil, fmt.Errorf("resolve target %q: %w", to, err)
		}
		if did, _ := resolved["did"].(string); did != "" {
			targetDID = did
		} else if doc, ok := resolved["did_document"].(map[string]any); ok {
			if id, _ := doc["id"].(string); id != "" {
				targetDID = id
			}
		}
	}
	meta := map[string]any{
		"profile":          DirectBaseProfile,
		"security_profile": DirectBaseSecurityProfile,
		"sender_did":       s.Active.DID,
		"target":           map[string]any{"kind": "agent", "did": targetDID},
		"operation_id":     messageID,
		"message_id":       messageID,
		"content_type":     contentType,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
	params := map[string]any{"meta": meta, "body": body}
	auth, err := proof.OriginProofAuth(s.Active, "direct.send", meta, body)
	if err != nil {
		return nil, err
	}
	params["auth"] = auth
	return s.Client.CallRaw(ctx, "direct.send", params)
}

func newMessageID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	return "msg_" + hex.EncodeToString(buffer)
}

// Sync pulls the backend inbox into the local store (best-effort when the
// backend is unreachable, the local store is still readable). Inbound E2EE
// messages are decrypted before persistence.
func (s *Service) Sync(ctx context.Context) ([]store.Message, error) {
	if s.Client == nil {
		return nil, nil
	}
	result, err := s.Client.CallRaw(ctx, "msg.inbox", map[string]any{"scope": "all"})
	if err != nil {
		return nil, err
	}
	rows, ok := result["messages"].([]any)
	if !ok {
		return nil, nil
	}
	synced := []store.Message{}
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		meta, _ := entry["meta"].(map[string]any)
		body, _ := entry["body"].(map[string]any)
		if meta == nil || body == nil {
			continue
		}
		messageID, _ := meta["message_id"].(string)
		if messageID == "" {
			continue
		}
		local, ok := s.normalizeWire(ctx, messageID, meta, body)
		if !ok {
			continue
		}
		// Skip echoes of our own outbound messages (already stored at send time).
		if local.SenderDID == s.Active.DID {
			continue
		}
		if err := store.UpsertMessage(s.DB, local); err != nil {
			continue
		}
		synced = append(synced, local)
		if local.SenderDID != "" && local.SenderDID != s.Active.DID {
			_ = store.UpsertContact(s.DB, store.Contact{DID: local.SenderDID, LastSeenAt: local.SentAt})
		}
	}
	return synced, nil
}

// secureAckBody is the reserved plaintext of an automated E2EE handshake ACK.
// It is sent by the responder after processing a direct_init so the initiator's
// session can leave pending-confirmation; it is never surfaced as a message.
const secureAckBody = "__anp_e2ee_ack__"

// normalizeWire converts an inbound envelope into a local message: group
// messages and transport-protected plaintext (direct base) are read directly,
// E2EE envelopes are decrypted via the direct_e2ee ratchet.
func (s *Service) normalizeWire(ctx context.Context, messageID string, meta, body map[string]any) (store.Message, bool) {
	senderDID, _ := meta["sender_did"].(string)
	profile, _ := meta["profile"].(string)
	if isGroupWire(meta, profile) {
		return s.normalizeGroupWire(messageID, meta, body, senderDID)
	}
	if profile == DirectBaseProfile {
		return s.normalizePlainWire(messageID, meta, body, senderDID)
	}
	// Skip anything that is not an E2EE direct envelope.
	if !e2ee.IsSecureWire(map[string]any{"meta": meta, "body": body}) {
		return store.Message{}, false
	}
	contentType, _ := meta["content_type"].(string)
	svc, err := s.ensureE2EE()
	if err != nil {
		return store.Message{}, false
	}
	result, err := svc.ProcessInbound(ctx, map[string]any{"meta": meta, "body": body})
	if err != nil {
		return store.Message{}, false
	}
	text, ok := e2ee.PlaintextText(result)
	if !ok {
		return store.Message{}, false
	}
	if contentType == e2ee.ContentTypeDirectInit {
		// Handshake: confirm the initiator's session with an encrypted ACK.
		s.sendAutoAck(ctx, senderDID)
	}
	if text == secureAckBody {
		// Control ACK from the peer: session confirmed, nothing to show.
		return store.Message{}, false
	}
	return store.Message{
		MessageID:    messageID,
		SenderDID:    senderDID,
		RecipientDID: s.Active.DID,
		Type:         "text",
		Text:         text,
		Secure:       true,
		Direction:    "in",
		Read:         false,
		SentAt:       time.Now().UTC().Format(time.RFC3339),
	}, true
}

// isGroupWire reports whether an inbound envelope is a group message.
func isGroupWire(meta map[string]any, profile string) bool {
	if profile == GroupBaseProfile {
		return true
	}
	target, _ := meta["target"].(map[string]any)
	kind, _ := target["kind"].(string)
	return kind == "group"
}

// normalizeGroupWire reads a transport-protected group message directly from
// the body without any decryption. P9 mention-bearing messages carry
// body.payload = {text, mentions, annotations?}; legacy messages carry
// body.text.
func (s *Service) normalizeGroupWire(messageID string, meta, body map[string]any, senderDID string) (store.Message, bool) {
	target, _ := meta["target"].(map[string]any)
	groupDID, _ := target["did"].(string)
	text, _ := body["text"].(string)
	var mentions []any
	if payload, ok := body["payload"].(map[string]any); ok {
		if payloadText, ok := payload["text"].(string); ok {
			text = payloadText
		}
		if rawMentions, ok := payload["mentions"].([]any); ok {
			mentions = rawMentions
		}
	}
	if groupDID == "" || strings.TrimSpace(text) == "" {
		return store.Message{}, false
	}
	return store.Message{
		MessageID: messageID,
		SenderDID: senderDID,
		GroupDID:  groupDID,
		Type:      "text",
		Text:      text,
		Mentions:  mentions,
		Secure:    false,
		Direction: "in",
		Read:      false,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
	}, true
}

// normalizePlainWire reads a transport-protected direct base message directly
// from the body without any decryption.
func (s *Service) normalizePlainWire(messageID string, meta, body map[string]any, senderDID string) (store.Message, bool) {
	text, _ := body["text"].(string)
	if strings.TrimSpace(text) == "" {
		return store.Message{}, false
	}
	return store.Message{
		MessageID:    messageID,
		SenderDID:    senderDID,
		RecipientDID: s.Active.DID,
		Type:         "text",
		Text:         text,
		Secure:       false,
		Direction:    "in",
		Read:         false,
		SentAt:       time.Now().UTC().Format(time.RFC3339),
	}, true
}

// sendAutoAck delivers an encrypted handshake ACK to the peer so their session
// leaves pending-confirmation. Best-effort: failures are not fatal.
func (s *Service) sendAutoAck(ctx context.Context, peerDID string) {
	svc, err := s.ensureE2EE()
	if err != nil {
		return
	}
	if _, err := svc.Send(ctx, peerDID, secureAckBody, newMessageID()); err != nil {
		return
	}
}

// Inbox returns local messages matching the filter.
func (s *Service) Inbox(filter store.MessageFilter) ([]store.Message, error) {
	return store.ListMessages(s.DB, filter)
}

// History returns the local thread with a peer.
func (s *Service) History(peer string, limit int) ([]store.Message, error) {
	return store.ListMessages(s.DB, store.MessageFilter{Peer: peer, Limit: limit})
}
