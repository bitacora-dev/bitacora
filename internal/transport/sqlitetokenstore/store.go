// Package sqlitetokenstore persists ingest bearer-token bindings in SQLite.
package sqlitetokenstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// Store implements transport.TokenStore using a SQLite database file.
type Store struct {
	db *sql.DB
}

var _ transport.TokenStore = (*Store)(nil)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS ingest_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		host_id    TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
}

// New opens path, creates parent directories when needed, and applies
// idempotent migrations.
func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating token store dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening token store: %w", err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("applying %q to token store: %w", pragma, err)
		}
	}

	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrating token store: %w", err)
		}
	}

	return &Store{db: db}, nil
}

// Close closes the underlying SQLite connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// AddToken hashes plaintextToken with Argon2id and binds it to hostID.
func (s *Store) AddToken(hostID, plaintextToken string) error {
	hash, err := transport.HashToken(plaintextToken)
	if err != nil {
		return fmt.Errorf("hashing token: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO ingest_tokens (host_id, token_hash, created_at)
		VALUES (?, ?, ?)
	`, hostID, hash, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("storing token for host %s: %w", hostID, err)
	}
	return nil
}

// Lookup implements transport.TokenStore.
func (s *Store) Lookup(ctx context.Context, token string) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT host_id, token_hash
		FROM ingest_tokens
		ORDER BY id ASC
	`)
	if err != nil {
		return "", false, fmt.Errorf("querying token hashes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}

		var hostID, hash string
		if err := rows.Scan(&hostID, &hash); err != nil {
			return "", false, fmt.Errorf("scanning token hash: %w", err)
		}

		match, err := transport.VerifyToken(token, hash)
		if err != nil {
			continue
		}
		if match {
			return hostID, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterating token hashes: %w", err)
	}
	return "", false, nil
}
