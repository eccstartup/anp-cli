// Package store provides the pure-Go SQLite workspace database for local
// message history, contacts, groups, and discovered agents.
package store

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the current local database schema version.
const SchemaVersion = 1

// DB aliases database/sql.DB so callers can use *store.DB directly with the
// store helpers.
type DB = sql.DB

func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := EnsureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	// The local database holds plaintext message bodies. Restrict the file to
	// the owner (0600) even if the workspace directory permissions are relaxed
	// later, instead of relying solely on the 0700 directory barrier.
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func EnsureSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_info (
			version INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL UNIQUE,
			sender_did TEXT NOT NULL,
			recipient_did TEXT,
			group_did TEXT,
			thread_id TEXT,
			type TEXT NOT NULL DEFAULT 'text',
			text TEXT,
			secure INTEGER NOT NULL DEFAULT 0,
			direction TEXT NOT NULL DEFAULT 'in',
			read INTEGER NOT NULL DEFAULT 0,
			sent_at TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_peer ON messages (recipient_did, sender_did);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_group ON messages (group_did);`,
		`CREATE TABLE IF NOT EXISTS contacts (
			did TEXT PRIMARY KEY,
			handle TEXT,
			name TEXT,
			last_seen_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS groups (
			group_did TEXT PRIMARY KEY,
			name TEXT,
			role TEXT,
			joined_at TEXT,
			members_json TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS discovered_agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			did TEXT UNIQUE,
			url TEXT,
			name TEXT,
			description TEXT,
			capabilities TEXT,
			ad_json TEXT,
			discovered_at TEXT NOT NULL
		);`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if err := upsertSchemaVersion(db, SchemaVersion); err != nil {
		return err
	}
	return nil
}

func upsertSchemaVersion(db *sql.DB, version int) error {
	if _, err := db.Exec(`DELETE FROM schema_info;`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO schema_info (version) VALUES (?);`, version)
	return err
}

func CurrentSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT version FROM schema_info LIMIT 1;`).Scan(&version)
	return version, err
}
