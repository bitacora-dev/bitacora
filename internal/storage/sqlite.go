package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// SQLiteStore is the default Relational backend (ADR-0003): WAL mode, one
// database file per month, a single writer goroutine so every write is
// serialized and readers never contend with each other over SQLite's write
// lock.
type SQLiteStore struct {
	dir string

	writeCh chan writeRequest
	closeCh chan struct{}
	wg      sync.WaitGroup

	mu  sync.Mutex
	dbs map[string]*sql.DB
}

var _ Relational = (*SQLiteStore)(nil)

type writeRequest struct {
	fn   func(ctx context.Context) error
	done chan error
}

// NewSQLiteStore opens (lazily, per month, on first use) SQLite databases
// under dir and starts the single writer goroutine.
func NewSQLiteStore(dir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating storage dir %s: %w", dir, err)
	}

	s := &SQLiteStore{
		dir:     dir,
		writeCh: make(chan writeRequest),
		closeCh: make(chan struct{}),
		dbs:     make(map[string]*sql.DB),
	}
	s.wg.Add(1)
	go s.writerLoop()
	return s, nil
}

func (s *SQLiteStore) writerLoop() {
	defer s.wg.Done()
	for {
		select {
		case req := <-s.writeCh:
			req.done <- req.fn(context.Background())
		case <-s.closeCh:
			return
		}
	}
}

// enqueueWrite is the only path any write takes — ADR-0003: "Todas las
// escrituras a SQLite pasan por una única goroutine con cola."
func (s *SQLiteStore) enqueueWrite(ctx context.Context, fn func(ctx context.Context) error) error {
	done := make(chan error, 1)
	select {
	case s.writeCh <- writeRequest{fn: fn, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closeCh:
		return fmt.Errorf("storage is closed")
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the writer goroutine and closes every open month database.
func (s *SQLiteStore) Close() error {
	close(s.closeCh)
	s.wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for _, db := range s.dbs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func monthKey(t time.Time) string {
	return t.UTC().Format("2006-01")
}

func (s *SQLiteStore) pathForMonth(month string) string {
	return filepath.Join(s.dir, fmt.Sprintf("events-%s.db", month))
}

// monthDB returns the (lazily opened, migrated, cached) *sql.DB for a
// month. Safe to call from any goroutine — opening/caching is guarded by a
// mutex independently of the write serialization above, since SQLite in
// WAL mode allows concurrent readers alongside the single writer.
func (s *SQLiteStore) monthDB(month string) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if db, ok := s.dbs[month]; ok {
		return db, nil
	}

	db, err := sql.Open("sqlite", s.pathForMonth(month))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", month, err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("applying %q to %s: %w", pragma, month, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating %s: %w", month, err)
	}

	s.dbs[month] = db
	return db, nil
}

// InsertEvent implements Relational.
func (s *SQLiteStore) InsertEvent(ctx context.Context, e schema.Event) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}

	month := monthKey(e.TS)
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.monthDB(month)
		if err != nil {
			return err
		}
		return insertEvent(ctx, db, e)
	})
}

func insertEvent(ctx context.Context, db *sql.DB, e schema.Event) error {
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

	res, err := db.ExecContext(ctx, `
		INSERT INTO events (id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, e.ID, e.TS.UnixMilli(), tsReceived, e.HostID, e.Source, e.Type, string(e.Severity), e.Title,
		string(subjectJSON), string(attrsJSON), e.Fingerprint, string(logRefsJSON), e.Schema)
	if err != nil {
		return fmt.Errorf("inserting event %s: %w", e.ID, err)
	}

	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking insert result for event %s: %w", e.ID, err)
	}
	if inserted == 0 {
		// Already present — inserting into events_fts again would create a
		// duplicate row, since FTS5 has no unique constraint on id.
		return nil
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO events_fts (id, title) VALUES (?, ?)`, e.ID, e.Title); err != nil {
		return fmt.Errorf("indexing event %s title: %w", e.ID, err)
	}
	return nil
}

func monthsBetween(from, to time.Time) []string {
	if to.Before(from) {
		return nil
	}
	var months []string
	seen := make(map[string]bool)
	for cur := from.UTC(); !cur.After(to.UTC()); cur = cur.AddDate(0, 1, 0) {
		key := monthKey(cur)
		if !seen[key] {
			seen[key] = true
			months = append(months, key)
		}
	}
	// AddDate stepping by whole months can undershoot the final month when
	// `to` and `from` land on different days of the month; make sure `to`
	// itself is always included.
	toKey := monthKey(to)
	if !seen[toKey] {
		months = append(months, toKey)
	}
	return months
}

// ListEvents implements Relational using SQLite's ATTACH DATABASE to query
// across every month file the range touches in one statement (ADR-0003).
// ATTACH is per-connection state, so this grabs one dedicated *sql.Conn for
// the whole operation instead of letting database/sql's pool hand out a
// different underlying connection between the ATTACH and the query.
func (s *SQLiteStore) ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error) {
	months := monthsBetween(from, to)
	if len(months) == 0 {
		return nil, nil
	}

	// Ensure every touched month has a migrated file to attach, including
	// months with no events yet.
	for _, m := range months {
		if _, err := s.monthDB(m); err != nil {
			return nil, fmt.Errorf("preparing month %s: %w", m, err)
		}
	}

	attachDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening attach connection: %w", err)
	}
	defer attachDB.Close()
	attachDB.SetMaxOpenConns(1)

	conn, err := attachDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring dedicated connection: %w", err)
	}
	defer conn.Close()

	var unionParts []string
	for i, m := range months {
		alias := fmt.Sprintf("m%d", i)
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE ? AS %s", alias), s.pathForMonth(m)); err != nil {
			return nil, fmt.Errorf("attaching %s: %w", m, err)
		}
		unionParts = append(unionParts, fmt.Sprintf("SELECT id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema FROM %s.events", alias))
	}

	query := fmt.Sprintf(`
		SELECT id, ts, ts_received, host_id, source, type, severity, title, subject_json, attrs_json, fingerprint, log_refs_json, schema
		FROM (%s)
		WHERE ts BETWEEN ? AND ? AND (? = '' OR host_id = ?)
		ORDER BY ts ASC
	`, strings.Join(unionParts, " UNION ALL "))

	rows, err := conn.QueryContext(ctx, query, from.UnixMilli(), to.UnixMilli(), hostID, hostID)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// SearchEventTitles implements Relational, demonstrating that FTS5 is
// available and wired up (ADR-0003's acceptance requirement), not just
// present as an unused virtual table.
func (s *SQLiteStore) SearchEventTitles(ctx context.Context, query string, limit int) ([]schema.Event, error) {
	s.mu.Lock()
	months := make([]string, 0, len(s.dbs))
	for m := range s.dbs {
		months = append(months, m)
	}
	s.mu.Unlock()

	var all []schema.Event
	for _, month := range months {
		db, err := s.monthDB(month)
		if err != nil {
			return nil, err
		}

		rows, err := db.QueryContext(ctx, `
			SELECT e.id, e.ts, e.ts_received, e.host_id, e.source, e.type, e.severity, e.title, e.subject_json, e.attrs_json, e.fingerprint, e.log_refs_json, e.schema
			FROM events_fts f
			JOIN events e ON e.id = f.id
			WHERE events_fts MATCH ?
			ORDER BY e.ts DESC
			LIMIT ?
		`, query, limit)
		if err != nil {
			return nil, fmt.Errorf("searching %s: %w", month, err)
		}
		found, err := scanEvents(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}

	return all, nil
}

func scanEvents(rows *sql.Rows) ([]schema.Event, error) {
	var events []schema.Event
	for rows.Next() {
		var (
			e                                             schema.Event
			tsMillis                                      int64
			tsReceivedMillis                              sql.NullInt64
			severity, subjectJSON, attrsJSON, logRefsJSON sql.NullString
		)
		if err := rows.Scan(&e.ID, &tsMillis, &tsReceivedMillis, &e.HostID, &e.Source, &e.Type, &severity, &e.Title,
			&subjectJSON, &attrsJSON, &logRefsJSON, &e.Fingerprint, &e.Schema); err != nil {
			return nil, fmt.Errorf("scanning event row: %w", err)
		}

		e.TS = time.UnixMilli(tsMillis).UTC()
		if tsReceivedMillis.Valid {
			e.TSReceived = time.UnixMilli(tsReceivedMillis.Int64).UTC()
		}
		e.Severity = schema.Severity(severity.String)

		if subjectJSON.Valid && subjectJSON.String != "" {
			_ = json.Unmarshal([]byte(subjectJSON.String), &e.Subject)
		}
		if attrsJSON.Valid && attrsJSON.String != "" {
			_ = json.Unmarshal([]byte(attrsJSON.String), &e.Attrs)
		}
		if logRefsJSON.Valid && logRefsJSON.String != "" {
			_ = json.Unmarshal([]byte(logRefsJSON.String), &e.LogRefs)
		}

		events = append(events, e)
	}
	return events, rows.Err()
}
