package store

import (
	"path/filepath"
	"testing"
)

func TestSchemaAndMessageRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "anp.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	version, err := CurrentSchemaVersion(db)
	if err != nil {
		t.Fatalf("CurrentSchemaVersion: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
	message := Message{
		MessageID:    "m1",
		SenderDID:    "did:wba:a:alice",
		RecipientDID: "did:wba:a:bob",
		Type:         "text",
		Text:         "hello",
		Direction:    "out",
		Read:         true,
	}
	if err := UpsertMessage(db, message); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}
	messages, err := ListMessages(db, MessageFilter{Scope: "direct", Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}
	if messages[0].Text != "hello" {
		t.Fatalf("text = %q, want hello", messages[0].Text)
	}
	// Upsert with the same id should not duplicate.
	if err := UpsertMessage(db, message); err != nil {
		t.Fatalf("UpsertMessage (2nd): %v", err)
	}
	messages, err = ListMessages(db, MessageFilter{Scope: "direct", Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages (2nd): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("duplicate upsert created %d rows", len(messages))
	}
}

func TestGroupAndContactRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "anp.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := UpsertGroup(db, Group{GroupDID: "g1", Name: "team", Role: "owner", Members: []any{"a", "b"}}); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	groups, err := ListGroups(db)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if err := UpsertContact(db, Contact{DID: "did:wba:x:y", Handle: "y", LastSeenAt: "t"}); err != nil {
		t.Fatalf("UpsertContact: %v", err)
	}
	contacts, err := ListContacts(db)
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(contacts) != 1 || contacts[0].Handle != "y" {
		t.Fatalf("unexpected contacts: %+v", contacts)
	}
	if err := DeleteGroup(db, "g1"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	groups, _ = ListGroups(db)
	if len(groups) != 0 {
		t.Fatalf("group not deleted")
	}
}

func TestDiscoveredAgentSearch(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "anp.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	agent := DiscoveredAgent{DID: "did:wba:x:agent:1", URL: "https://x/ad.json", Name: "OCR Service", Description: "converts images", Capabilities: []string{"ocr", "vision"}}
	if err := UpsertDiscoveredAgent(db, agent); err != nil {
		t.Fatalf("UpsertDiscoveredAgent: %v", err)
	}
	results, err := SearchDiscoveredAgents(db, "ocr", 10)
	if err != nil {
		t.Fatalf("SearchDiscoveredAgents: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Name != "OCR Service" {
		t.Fatalf("name = %q", results[0].Name)
	}
	if len(results[0].Capabilities) != 2 {
		t.Fatalf("capabilities = %v", results[0].Capabilities)
	}
}
