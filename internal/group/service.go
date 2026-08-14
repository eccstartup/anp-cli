// Package group implements ANP P4 group messaging base semantics: a thin
// signed client over the standard group.* JSON-RPC methods. Group messages and
// state changes are transport-protected plaintext (anp.group.base.v1) with an
// application-layer origin proof; there is no group E2EE path here.
package group

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/proof"
	"github.com/eccstartup/anp-cli/internal/transport"
)

const (
	GroupBaseProfile         = "anp.group.base.v1"
	GroupBaseSecurityProfile = "transport-protected"
)

// Service wraps the backend group.* methods with the active identity's signed
// client.
type Service struct {
	Config *config.Resolved
	Active *identity.Identity
	Client *transport.Client
}

func NewService(resolved *config.Resolved, active *identity.Identity, client *transport.Client) *Service {
	return &Service{Config: resolved, Active: active, Client: client}
}

type CreateOptions struct {
	Name         string         // display name (sets group_profile.display_name when profile is absent)
	GroupProfile map[string]any // full group_profile object
	Policy       map[string]any // full group_policy object (required)
}

// Create invokes group.create. The target is the group service DID.
func (s *Service) Create(ctx context.Context, opts CreateOptions) (map[string]any, error) {
	if opts.Policy == nil {
		return nil, fmt.Errorf("group policy is required")
	}
	groupProfile := opts.GroupProfile
	if groupProfile == nil {
		groupProfile = map[string]any{}
	}
	if strings.TrimSpace(opts.Name) != "" {
		if _, ok := groupProfile["display_name"]; !ok {
			groupProfile["display_name"] = opts.Name
		}
	}
	meta := s.meta("service", s.serviceDID(), newOperationID())
	body := map[string]any{"group_profile": groupProfile, "group_policy": opts.Policy}
	return s.call(ctx, "group.create", meta, body)
}

// GetInfo invokes group.get_info (no origin proof required).
func (s *Service) GetInfo(ctx context.Context, groupDID string, includePolicy, includeMembers bool) (map[string]any, error) {
	meta := s.meta("group", groupDID, newOperationID())
	body := map[string]any{}
	if includePolicy {
		body["include_policy"] = true
	}
	if includeMembers {
		body["include_member_list"] = true
	}
	return s.Client.CallRaw(ctx, "group.get_info", map[string]any{"meta": meta, "body": body})
}

// Join invokes group.join (open-join admission).
func (s *Service) Join(ctx context.Context, groupDID string) (map[string]any, error) {
	meta := s.meta("group", groupDID, newOperationID())
	return s.call(ctx, "group.join", meta, map[string]any{})
}

// Add invokes group.add with a member DID or handle.
func (s *Service) Add(ctx context.Context, groupDID, memberDID, memberHandle, role string) (map[string]any, error) {
	if strings.TrimSpace(memberDID) == "" && strings.TrimSpace(memberHandle) == "" {
		return nil, fmt.Errorf("--member-did or --member-handle is required")
	}
	meta := s.meta("group", groupDID, newOperationID())
	body := map[string]any{}
	if memberDID != "" {
		body["member_did"] = memberDID
	}
	if memberHandle != "" {
		body["member_handle"] = memberHandle
	}
	if role != "" {
		body["role"] = role
	}
	return s.call(ctx, "group.add", meta, body)
}

// Remove invokes group.remove for a member DID.
func (s *Service) Remove(ctx context.Context, groupDID, memberDID string) (map[string]any, error) {
	if strings.TrimSpace(memberDID) == "" {
		return nil, fmt.Errorf("--member-did is required")
	}
	meta := s.meta("group", groupDID, newOperationID())
	return s.call(ctx, "group.remove", meta, map[string]any{"member_did": memberDID})
}

// Leave invokes group.leave for the active identity.
func (s *Service) Leave(ctx context.Context, groupDID string) (map[string]any, error) {
	meta := s.meta("group", groupDID, newOperationID())
	return s.call(ctx, "group.leave", meta, map[string]any{})
}

// UpdateProfile applies an RFC 7386 JSON merge patch to the group profile.
func (s *Service) UpdateProfile(ctx context.Context, groupDID string, patch map[string]any) (map[string]any, error) {
	if patch == nil {
		return nil, fmt.Errorf("--patch is required")
	}
	meta := s.meta("group", groupDID, newOperationID())
	return s.call(ctx, "group.update_profile", meta, map[string]any{"group_profile_patch": patch})
}

// UpdatePolicy applies an RFC 7386 JSON merge patch to the group policy.
func (s *Service) UpdatePolicy(ctx context.Context, groupDID string, patch map[string]any) (map[string]any, error) {
	if patch == nil {
		return nil, fmt.Errorf("--patch is required")
	}
	meta := s.meta("group", groupDID, newOperationID())
	return s.call(ctx, "group.update_policy", meta, map[string]any{"group_policy_patch": patch})
}

type SendOptions struct {
	Text     string           // text/plain body
	Payload  map[string]any   // application/json body (takes precedence over Text)
	Mentions []map[string]any // P9 mentions array; when non-empty, body.payload = {text, mentions}
}

// Send delivers a transport-protected plaintext group message via group.send.
func (s *Service) Send(ctx context.Context, groupDID string, opts SendOptions) (map[string]any, error) {
	contentType := "text/plain"
	body := map[string]any{"text": opts.Text}
	switch {
	case len(opts.Mentions) > 0:
		// P9: body.payload = {text, mentions, annotations?}, meta.content_type = application/json.
		contentType = "application/json"
		payload := map[string]any{"text": opts.Text, "mentions": opts.Mentions}
		if annotations, ok := opts.Payload["annotations"]; ok && opts.Payload != nil {
			payload["annotations"] = annotations
		}
		body = map[string]any{"payload": payload}
	case opts.Payload != nil:
		contentType = "application/json"
		body = map[string]any{"payload": opts.Payload}
	default:
		if strings.TrimSpace(opts.Text) == "" {
			return nil, fmt.Errorf("message text is empty")
		}
	}
	messageID := newOperationID()
	meta := map[string]any{
		"profile":          GroupBaseProfile,
		"security_profile": GroupBaseSecurityProfile,
		"sender_did":       s.Active.DID,
		"target":           map[string]any{"kind": "group", "did": groupDID},
		"operation_id":     messageID,
		"message_id":       messageID,
		"content_type":     contentType,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
	return s.call(ctx, "group.send", meta, body)
}

// meta builds the common P4 meta object for a state-changing request.
func (s *Service) meta(targetKind, targetDID, operationID string) map[string]any {
	return map[string]any{
		"profile":          GroupBaseProfile,
		"security_profile": GroupBaseSecurityProfile,
		"sender_did":       s.Active.DID,
		"target":           map[string]any{"kind": targetKind, "did": targetDID},
		"operation_id":     operationID,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
}

// call signs meta/body with an origin proof and invokes the method.
func (s *Service) call(ctx context.Context, method string, meta, body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	params := map[string]any{"meta": meta, "body": body}
	auth, err := proof.OriginProofAuth(s.Active, method, meta, body)
	if err != nil {
		return nil, err
	}
	params["auth"] = auth
	return s.Client.CallRaw(ctx, method, params)
}

// serviceDID returns the group service DID used as group.create target.
func (s *Service) serviceDID() string {
	if strings.TrimSpace(s.Config.ServiceDID) != "" {
		return strings.TrimSpace(s.Config.ServiceDID)
	}
	domain := strings.TrimSpace(s.Config.DidDomain)
	if domain == "" {
		domain = hostnameForDID(s.Config.Backend)
	}
	if domain == "" {
		domain = "localhost"
	}
	return "did:wba:" + domain + ":service:anp"
}

func hostnameForDID(backend string) string {
	if backend == "" {
		return ""
	}
	parsed, err := url.Parse(backend)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func newOperationID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("op_%d", time.Now().UnixNano())
	}
	return "op_" + hex.EncodeToString(buffer)
}
