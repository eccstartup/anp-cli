// Package message implements direct and group messaging over the ANP wire
// protocol, with local SQLite persistence for inbox and history.
package message

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/e2ee"
	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/store"
	"github.com/ANPWorld/anp-cli/internal/transport"
	groupe2ee "github.com/agent-network-protocol/anp/golang/group_e2ee"
)

type SendOptions struct {
	To     string
	Group  string
	Type   string
	Text   string
	Secure bool
}

type Service struct {
	Config   *config.Resolved
	DB       *sql.DB
	Active   *identity.Identity
	Client   *transport.Client
	e2eeSvc  *e2ee.Service
	e2eeOnce sync.Once
	e2eeErr  error
}

func NewService(resolved *config.Resolved, db *sql.DB, active *identity.Identity, client *transport.Client) *Service {
	return &Service{Config: resolved, DB: db, Active: active, Client: client}
}

func (s *Service) ensureE2EE() (*e2ee.Service, error) {
	s.e2eeOnce.Do(func() {
		s.e2eeSvc, s.e2eeErr = e2ee.NewService(context.Background(), s.Config, s.Active, s.Client)
	})
	return s.e2eeSvc, s.e2eeErr
}

// ensureSecureReady registers the local DID document and publishes a prekey
// bundle so peers can establish secure sessions.
func (s *Service) ensureSecureReady() error {
	svc, err := s.ensureE2EE()
	if err != nil {
		return err
	}
	if _, err := s.Client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": s.Active.DID, "did_document": s.Active.DIDDocument,
	}); err != nil {
		return fmt.Errorf("register did document: %w", err)
	}
	if _, err := svc.PublishPrekeyBundle(); err != nil {
		return err
	}
	return nil
}

// Send delivers a message through the backend and persists an outbound record.
// With Secure set, the message is encrypted end-to-end via X3DH + ratchet.
func (s *Service) Send(ctx context.Context, options SendOptions) (*store.Message, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	if strings.TrimSpace(options.Text) == "" {
		return nil, fmt.Errorf("message text is empty")
	}
	if options.Secure {
		return s.sendSecure(ctx, options)
	}
	return s.sendPlain(ctx, options)
}

func (s *Service) sendPlain(ctx context.Context, options SendOptions) (*store.Message, error) {
	body := map[string]any{"type": "text", "text": options.Text}
	if options.Type != "" {
		body["type"] = options.Type
	}
	params := map[string]any{"body": body, "secure": false}
	if options.To != "" {
		params["to"] = options.To
	} else if options.Group != "" {
		params["group"] = options.Group
	} else {
		return nil, fmt.Errorf("either --to or --group is required")
	}
	result, err := s.Client.CallRaw(ctx, "msg.send", params)
	if err != nil {
		return nil, err
	}
	messageID, _ := result["message_id"].(string)
	threadID, _ := result["thread_id"].(string)
	sentAt, _ := result["sent_at"].(string)
	if messageID == "" {
		return nil, fmt.Errorf("backend returned no message_id")
	}
	if sentAt == "" {
		sentAt = time.Now().UTC().Format(time.RFC3339)
	}
	recipient := options.To
	groupDID := ""
	if options.Group != "" {
		groupDID = options.Group
		recipient = ""
	}
	local := store.Message{
		MessageID:    messageID,
		SenderDID:    s.Active.DID,
		RecipientDID: recipient,
		GroupDID:     groupDID,
		ThreadID:     threadID,
		Type:         options.Type,
		Text:         options.Text,
		Secure:       false,
		Direction:    "out",
		Read:         true,
		SentAt:       sentAt,
	}
	if local.Type == "" {
		local.Type = "text"
	}
	if err := store.UpsertMessage(s.DB, local); err != nil {
		return nil, err
	}
	return &local, nil
}

func (s *Service) sendSecure(ctx context.Context, options SendOptions) (*store.Message, error) {
	if options.Group != "" {
		// Group E2EE (P6 v2, MLS-based) is gated by the ANP SDK until the draft
		// MLS extension codepoint is stable. Surface the SDK's authoritative
		// block message instead of inventing our own group encryption.
		if err := groupe2ee.EnsureP6V2PublicReleaseReady(); err != nil {
			return nil, fmt.Errorf("group e2ee is unavailable: %w", err)
		}
		return nil, fmt.Errorf("group e2ee is not wired to the SDK P6 client yet")
	}
	if options.To == "" {
		return nil, fmt.Errorf("either --to or --group is required")
	}
	if err := s.ensureSecureReady(); err != nil {
		return nil, err
	}
	messageID := newMessageID()
	result, err := s.e2eeSvc.Send(ctx, options.To, options.Text, messageID)
	if err != nil {
		return nil, err
	}
	sentAt, _ := result["sent_at"].(string)
	if sentAt == "" {
		sentAt = time.Now().UTC().Format(time.RFC3339)
	}
	local := store.Message{
		MessageID:    messageID,
		SenderDID:    s.Active.DID,
		RecipientDID: options.To,
		Type:         "text",
		Text:         options.Text,
		Secure:       true,
		Direction:    "out",
		Read:         true,
		SentAt:       sentAt,
	}
	if err := store.UpsertMessage(s.DB, local); err != nil {
		return nil, err
	}
	return &local, nil
}

func newMessageID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	return "msg_" + hex.EncodeToString(buffer)
}

// wireMessage is the canonical backend message object shape.
type wireMessage struct {
	MessageID    string `json:"message_id"`
	SenderDID    string `json:"sender_did"`
	RecipientDID string `json:"recipient_did,omitempty"`
	GroupDID     string `json:"group_did,omitempty"`
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	Secure       bool   `json:"secure,omitempty"`
	SentAt       string `json:"sent_at,omitempty"`
	Meta         any    `json:"meta,omitempty"`
	Body         any    `json:"body,omitempty"`
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
		raw, _ := json.Marshal(row)
		var wire wireMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			continue
		}
		if wire.MessageID == "" {
			continue
		}
		local, ok := s.normalizeWire(ctx, wire)
		if !ok {
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

// normalizeWire converts an inbox item into a local message, decrypting E2EE
// payloads when present.
func (s *Service) normalizeWire(ctx context.Context, wire wireMessage) (store.Message, bool) {
	meta, _ := wire.Meta.(map[string]any)
	contentType, _ := meta["content_type"].(string)
	if contentType != "" {
		svc, err := s.ensureE2EE()
		if err != nil {
			return store.Message{}, false
		}
		if !e2ee.IsSecureWire(map[string]any{"meta": wire.Meta, "body": wire.Body}) {
			return store.Message{}, false
		}
		result, err := svc.ProcessInbound(ctx, map[string]any{"meta": wire.Meta, "body": wire.Body})
		if err != nil {
			return store.Message{}, false
		}
		text, ok := e2ee.PlaintextText(result)
		if !ok {
			return store.Message{}, false
		}
		senderDID, _ := meta["sender_did"].(string)
		if contentType == e2ee.ContentTypeDirectInit {
			// Handshake: confirm the initiator's session with an encrypted ACK.
			s.sendAutoAck(ctx, senderDID)
		}
		if text == secureAckBody {
			// Control ACK from the peer: session confirmed, nothing to show.
			return store.Message{}, false
		}
		return store.Message{
			MessageID:    wire.MessageID,
			SenderDID:    senderDID,
			RecipientDID: s.Active.DID,
			Type:         "text",
			Text:         text,
			Secure:       true,
			Direction:    "in",
			Read:         false,
			SentAt:       wire.SentAt,
		}, true
	}
	local := store.Message{
		MessageID:    wire.MessageID,
		SenderDID:    wire.SenderDID,
		RecipientDID: wire.RecipientDID,
		GroupDID:     wire.GroupDID,
		Type:         wire.Type,
		Text:         wire.Text,
		Secure:       wire.Secure,
		Direction:    "in",
		Read:         false,
		SentAt:       wire.SentAt,
	}
	if local.Type == "" {
		local.Type = "text"
	}
	return local, true
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
