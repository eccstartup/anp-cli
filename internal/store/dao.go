package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type Message struct {
	MessageID    string `json:"message_id"`
	SenderDID    string `json:"sender_did"`
	RecipientDID string `json:"recipient_did,omitempty"`
	GroupDID     string `json:"group_did,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	Type         string `json:"type"`
	Text         string `json:"text"`
	Mentions     []any  `json:"mentions,omitempty"`
	Secure       bool   `json:"secure"`
	Direction    string `json:"direction"`
	Read         bool   `json:"read"`
	SentAt       string `json:"sent_at,omitempty"`
}

type MessageFilter struct {
	Scope      string // all | direct | group
	Peer       string // direct peer DID
	Group      string
	UnreadOnly bool
	Limit      int
}

type Contact struct {
	DID        string `json:"did"`
	Handle     string `json:"handle,omitempty"`
	Name       string `json:"name,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

type Group struct {
	GroupDID string `json:"group_did"`
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	JoinedAt string `json:"joined_at,omitempty"`
	Members  []any  `json:"members,omitempty"`
}

type DiscoveredAgent struct {
	DID          string         `json:"did,omitempty"`
	URL          string         `json:"url"`
	Name         string         `json:"name,omitempty"`
	Description  string         `json:"description,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	AD           map[string]any `json:"ad,omitempty"`
	DiscoveredAt string         `json:"discovered_at"`
}

func UpsertMessage(db *sql.DB, message Message) error {
	mentionsJSON, err := marshalNullable(message.Mentions)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO messages (message_id, sender_did, recipient_did, group_did, thread_id, type, text, mentions_json, secure, direction, read, sent_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET text=excluded.text, mentions_json=excluded.mentions_json`,
		message.MessageID, message.SenderDID, nullIfEmpty(message.RecipientDID), nullIfEmpty(message.GroupDID), nullIfEmpty(message.ThreadID),
		message.Type, message.Text, mentionsJSON, boolInt(message.Secure), message.Direction, boolInt(message.Read), message.SentAt, time.Now().UTC().Format(time.RFC3339))
	return err
}

// marshalNullable marshals a value to JSON, returning SQL NULL for an empty
// slice or nil value.
func marshalNullable(value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []any:
		if len(typed) == 0 {
			return nil, nil
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func ListMessages(db *sql.DB, filter MessageFilter) ([]Message, error) {
	where := []string{}
	args := []any{}
	if filter.Scope == "direct" {
		where = append(where, "group_did IS NULL")
	}
	if filter.Scope == "group" {
		where = append(where, "group_did IS NOT NULL")
	}
	if filter.Peer != "" {
		where = append(where, "(sender_did = ? OR recipient_did = ?)")
		args = append(args, filter.Peer, filter.Peer)
	}
	if filter.Group != "" {
		where = append(where, "group_did = ?")
		args = append(args, filter.Group)
	}
	if filter.UnreadOnly {
		where = append(where, "read = 0")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	query := "SELECT message_id, sender_did, recipient_did, group_did, thread_id, type, text, mentions_json, secure, direction, read, sent_at FROM messages"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		var (
			message      Message
			recipient    sql.NullString
			group        sql.NullString
			thread       sql.NullString
			mentionsJSON sql.NullString
		)
		if err := rows.Scan(&message.MessageID, &message.SenderDID, &recipient, &group, &thread, &message.Type, &message.Text, &mentionsJSON, &message.Secure, &message.Direction, &message.Read, &message.SentAt); err != nil {
			return nil, err
		}
		message.RecipientDID = recipient.String
		message.GroupDID = group.String
		message.ThreadID = thread.String
		if mentionsJSON.Valid && mentionsJSON.String != "" {
			_ = json.Unmarshal([]byte(mentionsJSON.String), &message.Mentions)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func UpsertContact(db *sql.DB, contact Contact) error {
	_, err := db.Exec(`
		INSERT INTO contacts (did, handle, name, last_seen_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(did) DO UPDATE SET handle=COALESCE(excluded.handle, contacts.handle), name=COALESCE(excluded.name, contacts.name), last_seen_at=excluded.last_seen_at`,
		contact.DID, contact.Handle, contact.Name, contact.LastSeenAt)
	return err
}

func ListContacts(db *sql.DB) ([]Contact, error) {
	rows, err := db.Query(`SELECT did, handle, name, last_seen_at FROM contacts ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := []Contact{}
	for rows.Next() {
		var contact Contact
		if err := rows.Scan(&contact.DID, &contact.Handle, &contact.Name, &contact.LastSeenAt); err != nil {
			return nil, err
		}
		contacts = append(contacts, contact)
	}
	return contacts, rows.Err()
}

func UpsertGroup(db *sql.DB, group Group) error {
	membersJSON, err := json.Marshal(group.Members)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO groups (group_did, name, role, joined_at, members_json) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(group_did) DO UPDATE SET name=excluded.name, role=excluded.role, members_json=excluded.members_json`,
		group.GroupDID, group.Name, group.Role, group.JoinedAt, string(membersJSON))
	return err
}

func ListGroups(db *sql.DB) ([]Group, error) {
	rows, err := db.Query(`SELECT group_did, name, role, joined_at, members_json FROM groups ORDER BY joined_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []Group{}
	for rows.Next() {
		var group Group
		var membersJSON string
		if err := rows.Scan(&group.GroupDID, &group.Name, &group.Role, &group.JoinedAt, &membersJSON); err != nil {
			return nil, err
		}
		if membersJSON != "" {
			_ = json.Unmarshal([]byte(membersJSON), &group.Members)
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func DeleteGroup(db *sql.DB, groupDID string) error {
	_, err := db.Exec(`DELETE FROM groups WHERE group_did = ?`, groupDID)
	return err
}

func UpsertDiscoveredAgent(db *sql.DB, agent DiscoveredAgent) error {
	capabilities, _ := json.Marshal(agent.Capabilities)
	adJSON, err := json.Marshal(agent.AD)
	if err != nil {
		return err
	}
	if agent.DiscoveredAt == "" {
		agent.DiscoveredAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err = db.Exec(`
		INSERT INTO discovered_agents (did, url, name, description, capabilities, ad_json, discovered_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(did) DO UPDATE SET url=excluded.url, name=excluded.name, description=excluded.description, capabilities=excluded.capabilities, ad_json=excluded.ad_json, discovered_at=excluded.discovered_at`,
		agent.DID, agent.URL, agent.Name, agent.Description, string(capabilities), string(adJSON), agent.DiscoveredAt)
	return err
}

func SearchDiscoveredAgents(db *sql.DB, query string, limit int) ([]DiscoveredAgent, error) {
	if limit <= 0 {
		limit = 20
	}
	like := "%" + query + "%"
	rows, err := db.Query(`
		SELECT did, url, name, description, capabilities, ad_json, discovered_at FROM discovered_agents
		WHERE name LIKE ? OR description LIKE ? OR capabilities LIKE ? OR did LIKE ?
		ORDER BY discovered_at DESC LIMIT ?`, like, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []DiscoveredAgent{}
	for rows.Next() {
		var agent DiscoveredAgent
		var capabilities string
		var adJSON string
		if err := rows.Scan(&agent.DID, &agent.URL, &agent.Name, &agent.Description, &capabilities, &adJSON, &agent.DiscoveredAt); err != nil {
			return nil, err
		}
		if capabilities != "" {
			_ = json.Unmarshal([]byte(capabilities), &agent.Capabilities)
		}
		if adJSON != "" && adJSON != "null" {
			_ = json.Unmarshal([]byte(adJSON), &agent.AD)
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
