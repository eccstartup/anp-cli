// Package group implements group lifecycle operations over the ANP wire
// protocol, with local persistence.
package group

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/store"
	"github.com/eccstartup/anp-cli/internal/transport"
)

type Service struct {
	DB     *sql.DB
	Active *identity.Identity
	Client *transport.Client
}

func NewService(db *sql.DB, active *identity.Identity, client *transport.Client) *Service {
	return &Service{DB: db, Active: active, Client: client}
}

func (s *Service) Create(ctx context.Context, name string, members string) (map[string]any, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	params := map[string]any{"name": name}
	if members != "" {
		var list []any
		if err := json.Unmarshal([]byte(members), &list); err != nil {
			return nil, fmt.Errorf("--members must be a JSON array: %w", err)
		}
		params["members"] = list
	}
	result, err := s.Client.CallRaw(ctx, "group.create", params)
	if err != nil {
		return nil, err
	}
	if groupDID, _ := result["group_did"].(string); groupDID != "" {
		_ = store.UpsertGroup(s.DB, store.Group{
			GroupDID: groupDID,
			Name:     name,
			Role:     "owner",
			JoinedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func (s *Service) Join(ctx context.Context, groupDID string) (map[string]any, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	result, err := s.Client.CallRaw(ctx, "group.join", map[string]any{"group": groupDID})
	if err != nil {
		return nil, err
	}
	_ = store.UpsertGroup(s.DB, store.Group{
		GroupDID: groupDID,
		Role:     "member",
		JoinedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return result, nil
}

func (s *Service) Leave(ctx context.Context, groupDID string) (map[string]any, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	result, err := s.Client.CallRaw(ctx, "group.leave", map[string]any{"group": groupDID})
	if err != nil {
		return nil, err
	}
	_ = store.DeleteGroup(s.DB, groupDID)
	return result, nil
}

func (s *Service) Members(ctx context.Context, groupDID string) ([]map[string]any, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or config backend")
	}
	result, err := s.Client.CallRaw(ctx, "group.members", map[string]any{"group": groupDID})
	if err != nil {
		return nil, err
	}
	members := []map[string]any{}
	rows, _ := result["members"].([]any)
	for _, row := range rows {
		if member, ok := row.(map[string]any); ok {
			members = append(members, member)
		}
	}
	return members, nil
}

func (s *Service) LocalGroups() ([]store.Group, error) {
	return store.ListGroups(s.DB)
}
