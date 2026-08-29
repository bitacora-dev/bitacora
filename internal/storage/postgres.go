package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// postgresMigrations creates the events table with the PostgreSQL-native
// indexing ADR-0003 calls for: BRIN over the (naturally time-ordered)
// timestamp column, GIN over the JSONB attrs for the flexible-attribute
// case, and a GIN full-text index over title as the FTS5 equivalent.
//
// Unlike SQLite, there's no monthly-file partitioning here yet — this
// implementation is scoped to matching SQLite's Relational behavior
// (this task's acceptance criterion), not native table partitioning,
// which ADR-0003 mentions but doesn't require for this task. Flagged as
// a followup.
var postgresMigrations = []string{
	`CREATE TABLE IF NOT EXISTS events (
		id            TEXT PRIMARY KEY,
		ts            BIGINT NOT NULL,
		ts_received   BIGINT,
		host_id       TEXT NOT NULL,
		source        TEXT NOT NULL,
		type          TEXT NOT NULL,
		severity      TEXT NOT NULL,
		title         TEXT NOT NULL,
		subject_json  JSONB,
		attrs_json    JSONB,
		fingerprint   TEXT,
		log_refs_json JSONB,
		schema        INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_events_ts_brin ON events USING BRIN (ts)`,
	`CREATE INDEX IF NOT EXISTS idx_events_host_ts ON events (host_id, ts)`,
	`CREATE INDEX IF NOT EXISTS idx_events_attrs_gin ON events USING GIN (attrs_json)`,
	`CREATE INDEX IF NOT EXISTS idx_events_title_fts ON events USING GIN (to_tsvector('english', title))`,
}

// PostgresStore is the optional Relational backend (ADR-0003): same
// interface and behavior as SQLiteStore, kept green in CI alongside it
// from day one, per that ADR's explicit "no es un plan para después"
// requirement.
//
// Unlike SQLite, PostgreSQL handles concurrent writers natively — there's
// no single-writer-goroutine queue here, because that serialization in
// SQLiteStore exists specifically to work around SQLite's single-writer
// lock, a problem PostgreSQL doesn't have.
type PostgresStore struct {
	db *sql.DB
}

var _ Relational = (*PostgresStore)(nil)

// NewPostgresStore opens a connection pool to dsn (a standard PostgreSQL
// connection string, e.g. "postgres://user@host:port/dbname?sslmode=disable")
// and runs migrations.
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	for _, stmt := range postgresMigrations {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrating postgres: %w", err)
		}
	}

	return &PostgresStore{db: db}, nil
}

// Close implements Relational.
func (s *PostgresStore) Close() error { return s.db.Close() }

// InsertEvent implements Relational.
func (s *PostgresStore) InsertEvent(ctx context.Context, e schema.Event) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}

	subjectJSON, err := json.Marshal(e.Subject)
	if err != nil {
		return fmt.Errorf("marshaling subject: %w", err)
	}
	attrsJSON, err := json.Marshal(e.Attrs)
	if err != nil {
		return fmt.Errorf("marshaling attrs: %w", err)
	}
	logRefsJSON, err := json.Marshal(e.LogRefs)
	if err != nil {
		return fmt.Errorf("marshaling log_refs: %w", err)
	}

	var tsReceived any
	if !e.TSReceived.IsZero() {
		tsReceived = e.TSReceived.UnixMilli()
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO events (id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO NOTHING
	`, e.ID, e.TS.UnixMilli(), tsReceived, e.HostID, e.Source, e.Type, string(e.Severity), e.Title,
		string(subjectJSON), string(attrsJSON), e.Fingerprint, string(logRefsJSON), e.Schema)
	if err != nil {
		return fmt.Errorf("inserting event %s: %w", e.ID, err)
	}
	return nil
}

// ListEvents implements Relational.
func (s *PostgresStore) ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema
		FROM events
		WHERE ts BETWEEN $1 AND $2 AND ($3 = '' OR host_id = $3)
		ORDER BY ts ASC
	`, from.UnixMilli(), to.UnixMilli(), hostID)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// SearchEventTitles implements Relational using PostgreSQL full-text
// search (the GIN index in postgresMigrations) — the equivalent of
// SQLite's FTS5 for this backend.
func (s *PostgresStore) SearchEventTitles(ctx context.Context, query string, limit int) ([]schema.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema
		FROM events
		WHERE to_tsvector('english', title) @@ plainto_tsquery('english', $1)
		ORDER BY ts DESC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}
