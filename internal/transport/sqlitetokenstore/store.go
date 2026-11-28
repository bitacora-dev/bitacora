// Package sqlitetokenstore persists ingest bearer-token bindings in SQLite.
package sqlitetokenstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// lastSeenWriteInterval throttles last_seen_at writes. Every authenticated
// ingest request is a candidate for one, at the ten-second cadence of
// ADR-0008 times one row per agent — turning a read-mostly auth path into a
// steady write load, with the WAL growth and fsync cost that follows, to
// answer a question ("when did this agent last talk to us") nobody asks at
// second resolution. One write per minute per credential is enough for the
// operator surface it feeds.
const lastSeenWriteInterval = time.Minute

// Store implements transport.TokenStore using a SQLite database file.
type Store struct {
	db *sql.DB

	// now is the clock, injectable so tests can exercise a rotation grace
	// period and the last-seen throttle without sleeping.
	now func() time.Time

	// lastSeenMu guards lastSeenWrites, the in-process record of when each
	// credential's last_seen_at was last persisted. It is deliberately not
	// durable: losing it on restart costs at most one extra write.
	lastSeenMu     sync.Mutex
	lastSeenWrites map[int64]time.Time
}

var (
	_ transport.TokenStore      = (*Store)(nil)
	_ transport.TokenAdmin      = (*Store)(nil)
	_ transport.EnrollmentStore = (*Store)(nil)
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS ingest_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		host_id    TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS device_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS enrollment_codes (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		code_hash        TEXT NOT NULL,
		label            TEXT NOT NULL DEFAULT '',
		expected_host_id TEXT NOT NULL DEFAULT '',
		created_at       INTEGER NOT NULL,
		expires_at       INTEGER NOT NULL,
		consumed_at      INTEGER
	)`,
}

// columnAdditions are the lifecycle columns ADR-0008 needs on top of the
// original three-column ingest_tokens table. They are applied separately
// from migrations because SQLite has no "ADD COLUMN IF NOT EXISTS": running
// the ALTER twice is an error, and this store must open an existing
// tokens.db as happily as an empty one. addMissingColumns reads the live
// schema instead of tracking a version number, so a database written by any
// earlier build converges to the same shape.
//
// Every addition is nullable or has a default, which is what keeps the
// migration backward compatible: the tokens already in the file stay valid
// (revoked_at and expires_at NULL is exactly "active, never retired") and
// an older binary reading the same file still finds the columns it knows.
var columnAdditions = []struct{ table, column, definition string }{
	{"ingest_tokens", "label", "TEXT NOT NULL DEFAULT ''"},
	{"ingest_tokens", "last_seen_at", "INTEGER"},
	{"ingest_tokens", "revoked_at", "INTEGER"},
	{"ingest_tokens", "expires_at", "INTEGER"},
	{"ingest_tokens", "rotated_from", "INTEGER"},
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
	if err := addMissingColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := restrictMode(path); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{
		db:             db,
		now:            time.Now,
		lastSeenWrites: make(map[int64]time.Time),
	}, nil
}

// addMissingColumns brings an existing tokens.db up to the current shape,
// adding only the columns it does not already have.
func addMissingColumns(db *sql.DB) error {
	for _, addition := range columnAdditions {
		has, err := hasColumn(db, addition.table, addition.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", addition.table, addition.column, addition.definition)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("adding column %s.%s to token store: %w", addition.table, addition.column, err)
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	// table_info takes an identifier, not a bindable value, and both
	// arguments here come from the columnAdditions literal above — never
	// from input.
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("reading %s schema: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &dflt, &primaryKey); err != nil {
			return false, fmt.Errorf("scanning %s schema: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterating %s schema: %w", table, err)
	}
	return false, nil
}

// restrictMode narrows tokens.db and its WAL sidecars to 0600. ADR-0008
// fixes that mode for the token file on the agent, and the hub's side of the
// same credential has no reason to be more readable: the file holds every
// Argon2id hash in the installation. SQLite creates it at 0644 minus the
// umask, which on a default Debian umask is world-readable.
//
// A permission error is tolerated on purpose. The hub runs as the
// unprivileged bitacora user (ADR-0005) and `bita agent` is run by an
// operator through that same user, but a file left behind by root — an
// earlier `bitacora-hub -add-token` run as root, say — cannot be chmodded by
// the daemon, and refusing to start over the mode of a file that is already
// readable only by root would be a worse outcome than continuing.
func restrictMode(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stating %s: %w", candidate, err)
		}
		if info.Mode().Perm()&^0o600 == 0 {
			continue
		}
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("restricting %s to 0600: %w", candidate, err)
		}
	}
	return nil
}

// Close closes the underlying SQLite connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// AddToken hashes plaintextToken with Argon2id and binds it to hostID. It
// is the unlabelled form kept for `bitacora-hub -add-token` and for
// hubapi.HostRegistrar, whose callers have no name to give the credential.
func (s *Store) AddToken(hostID, plaintextToken string) error {
	_, err := s.AddLabeledToken(context.Background(), hostID, "", plaintextToken)
	return err
}

// AddLabeledToken implements transport.TokenAdmin.
func (s *Store) AddLabeledToken(ctx context.Context, hostID, label, plaintextToken string) (transport.TokenInfo, error) {
	return s.insertToken(ctx, hostID, label, plaintextToken, 0)
}

// insertToken is the single write path for an ingest credential, so the
// hashing and the "plaintext is never persisted" guarantee live in one
// place. rotatedFrom is 0 for a fresh credential.
func (s *Store) insertToken(ctx context.Context, hostID, label, plaintextToken string, rotatedFrom int64) (transport.TokenInfo, error) {
	if plaintextToken == "" {
		return transport.TokenInfo{}, errors.New("refusing to store an empty ingest token")
	}
	hash, err := transport.HashToken(plaintextToken)
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("hashing token: %w", err)
	}

	createdAt := s.clock().UTC()
	var from any
	if rotatedFrom != 0 {
		from = rotatedFrom
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO ingest_tokens (host_id, token_hash, created_at, label, rotated_from)
		VALUES (?, ?, ?, ?, ?)
	`, hostID, hash, createdAt.UnixMilli(), label, from)
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("storing token for host %s: %w", hostID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("reading stored token id for host %s: %w", hostID, err)
	}
	return transport.TokenInfo{
		ID:          id,
		HostID:      hostID,
		Label:       label,
		CreatedAt:   createdAt,
		RotatedFrom: rotatedFrom,
	}, nil
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// AddDeviceToken hashes plaintextToken with Argon2id and persists it for a
// reading device. Device tokens deliberately have no host_id: unlike ingest
// credentials, they authorize a browser or PWA to read the hub (ADR-0014).
func (s *Store) AddDeviceToken(plaintextToken string) error {
	hash, err := transport.HashToken(plaintextToken)
	if err != nil {
		return fmt.Errorf("hashing device token: %w", err)
	}

	if _, err := s.db.Exec(`
		INSERT INTO device_tokens (token_hash, created_at)
		VALUES (?, ?)
	`, hash, time.Now().UTC().UnixMilli()); err != nil {
		return fmt.Errorf("storing device token: %w", err)
	}
	return nil
}

// HasAnyDeviceToken reports whether at least one device token exists.
func (s *Store) HasAnyDeviceToken(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_tokens)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking device tokens: %w", err)
	}
	return exists, nil
}

// LookupDeviceToken verifies token against the persisted device-token hashes.
func (s *Store) LookupDeviceToken(ctx context.Context, token string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token_hash
		FROM device_tokens
		ORDER BY id ASC
	`)
	if err != nil {
		return false, fmt.Errorf("querying device token hashes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		var hash string
		if err := rows.Scan(&hash); err != nil {
			return false, fmt.Errorf("scanning device token hash: %w", err)
		}
		match, err := transport.VerifyToken(token, hash)
		if err != nil {
			continue
		}
		if match {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterating device token hashes: %w", err)
	}
	return false, nil
}

// Lookup implements transport.TokenStore.
//
// Revoked and grace-expired credentials are excluded by the query itself,
// which is what makes a revocation take effect promptly: there is no cache
// of verified tokens anywhere on the ingest path, so the very next request
// re-reads this table and is refused. Adding a hit cache here would be a
// measurable saving on the Argon2id verify and would also hand a revoked
// agent a window in which it still ingests — if one is ever added it must
// carry a TTL short enough that "revoked" and "rejected" stay close
// together, and RevokeHostTokens must invalidate it outright.
func (s *Store) Lookup(ctx context.Context, token string) (string, bool, error) {
	now := s.clock().UTC()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, host_id, token_hash
		FROM ingest_tokens
		WHERE revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY id ASC
	`, now.UnixMilli())
	if err != nil {
		return "", false, fmt.Errorf("querying token hashes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}

		var (
			id           int64
			hostID, hash string
		)
		if err := rows.Scan(&id, &hostID, &hash); err != nil {
			return "", false, fmt.Errorf("scanning token hash: %w", err)
		}

		match, err := transport.VerifyToken(token, hash)
		if err != nil {
			continue
		}
		if match {
			// Close the cursor before writing: SQLite will not take the
			// write lock while this read is still open.
			rows.Close()
			s.noteLastSeen(ctx, id, now)
			return hostID, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterating token hashes: %w", err)
	}
	return "", false, nil
}

// noteLastSeen records a successful authentication without ever letting that
// bookkeeping decide the outcome of the authentication itself. A failed
// last_seen_at write means the operator listing shows a staler timestamp;
// turning it into a rejected ingest batch would trade telemetry for a
// cosmetic field, so the error is dropped here and nowhere else.
func (s *Store) noteLastSeen(ctx context.Context, id int64, now time.Time) {
	_ = s.TouchLastSeen(ctx, id, now)
}

// TouchLastSeen records that the credential with this id authenticated a
// request at now, at most once per lastSeenWriteInterval per credential.
// A throttled call is a no-op and reports no error: the caller asked for the
// timestamp to be fresh enough, not for a write.
func (s *Store) TouchLastSeen(ctx context.Context, id int64, now time.Time) error {
	s.lastSeenMu.Lock()
	if s.lastSeenWrites == nil {
		s.lastSeenWrites = make(map[int64]time.Time)
	}
	if last, ok := s.lastSeenWrites[id]; ok && now.Sub(last) < lastSeenWriteInterval {
		s.lastSeenMu.Unlock()
		return nil
	}
	s.lastSeenWrites[id] = now
	s.lastSeenMu.Unlock()

	if _, err := s.db.ExecContext(ctx, `
		UPDATE ingest_tokens SET last_seen_at = ? WHERE id = ?
	`, now.UTC().UnixMilli(), id); err != nil {
		// Let the next request retry instead of waiting out the interval
		// on a write that never landed.
		s.lastSeenMu.Lock()
		delete(s.lastSeenWrites, id)
		s.lastSeenMu.Unlock()
		return fmt.Errorf("recording last_seen_at for token %d: %w", id, err)
	}
	return nil
}

// ListTokens implements transport.TokenAdmin.
func (s *Store) ListTokens(ctx context.Context) ([]transport.TokenInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, host_id, label, created_at, last_seen_at, revoked_at, expires_at, rotated_from
		FROM ingest_tokens
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing ingest tokens: %w", err)
	}
	defer rows.Close()

	var tokens []transport.TokenInfo
	for rows.Next() {
		var (
			info       transport.TokenInfo
			createdAt  int64
			lastSeen   sql.NullInt64
			revokedAt  sql.NullInt64
			expiresAt  sql.NullInt64
			rotatedFrm sql.NullInt64
		)
		if err := rows.Scan(&info.ID, &info.HostID, &info.Label, &createdAt, &lastSeen, &revokedAt, &expiresAt, &rotatedFrm); err != nil {
			return nil, fmt.Errorf("scanning ingest token: %w", err)
		}
		info.CreatedAt = time.UnixMilli(createdAt).UTC()
		info.LastSeenAt = optionalTime(lastSeen)
		info.RevokedAt = optionalTime(revokedAt)
		info.ExpiresAt = optionalTime(expiresAt)
		info.RotatedFrom = rotatedFrm.Int64
		tokens = append(tokens, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating ingest tokens: %w", err)
	}
	return tokens, nil
}

func optionalTime(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.UnixMilli(value.Int64).UTC()
}

// RevokeHostTokens implements transport.TokenAdmin.
func (s *Store) RevokeHostTokens(ctx context.Context, hostID string) (int, error) {
	if hostID == "" {
		return 0, errors.New("revoking requires a host_id")
	}
	revoked, err := s.retireHostTokens(ctx, s.db, hostID, 0)
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

// execer is satisfied by both *sql.DB and *sql.Tx, so retireHostTokens can
// run standalone or inside the rotation transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// retireHostTokens takes every still-active credential of hostID out of
// service. grace > 0 sets an expiry that far ahead instead of revoking now,
// which is the rotation window; grace <= 0 revokes immediately.
//
// Only active rows are touched, so an already-revoked credential keeps its
// original revocation instant: a second revoke must not rewrite history.
func (s *Store) retireHostTokens(ctx context.Context, db execer, hostID string, grace time.Duration) (int, error) {
	now := s.clock().UTC()
	var (
		result sql.Result
		err    error
	)
	if grace > 0 {
		result, err = db.ExecContext(ctx, `
			UPDATE ingest_tokens
			SET expires_at = ?
			WHERE host_id = ?
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > ?)
		`, now.Add(grace).UnixMilli(), hostID, now.UnixMilli())
	} else {
		result, err = db.ExecContext(ctx, `
			UPDATE ingest_tokens
			SET revoked_at = ?
			WHERE host_id = ?
			  AND revoked_at IS NULL
		`, now.UnixMilli(), hostID)
	}
	if err != nil {
		return 0, fmt.Errorf("retiring tokens of host %s: %w", hostID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting retired tokens of host %s: %w", hostID, err)
	}
	return int(affected), nil
}

// RotateHostTokens implements transport.TokenAdmin. The retirement of the
// old credentials and the insertion of the new one share a transaction: a
// failure must never leave a host with no usable credential, and must never
// leave two live ones either.
func (s *Store) RotateHostTokens(ctx context.Context, hostID, label, plaintextToken string, grace time.Duration) (transport.TokenInfo, error) {
	if hostID == "" {
		return transport.TokenInfo{}, errors.New("rotating requires a host_id")
	}

	previous, err := s.latestTokenOf(ctx, hostID)
	if err != nil {
		return transport.TokenInfo{}, err
	}
	if previous.ID == 0 {
		return transport.TokenInfo{}, fmt.Errorf("rotating host %s: %w", hostID, transport.ErrNoCredential)
	}
	if label == "" {
		label = previous.Label
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("beginning rotation for host %s: %w", hostID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.retireHostTokens(ctx, tx, hostID, grace); err != nil {
		return transport.TokenInfo{}, err
	}

	if plaintextToken == "" {
		return transport.TokenInfo{}, errors.New("refusing to store an empty ingest token")
	}
	hash, err := transport.HashToken(plaintextToken)
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("hashing token: %w", err)
	}
	createdAt := s.clock().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_tokens (host_id, token_hash, created_at, label, rotated_from)
		VALUES (?, ?, ?, ?, ?)
	`, hostID, hash, createdAt.UnixMilli(), label, previous.ID)
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("storing rotated token for host %s: %w", hostID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("reading rotated token id for host %s: %w", hostID, err)
	}
	if err := tx.Commit(); err != nil {
		return transport.TokenInfo{}, fmt.Errorf("committing rotation for host %s: %w", hostID, err)
	}

	return transport.TokenInfo{
		ID:          id,
		HostID:      hostID,
		Label:       label,
		CreatedAt:   createdAt,
		RotatedFrom: previous.ID,
	}, nil
}

// latestTokenOf returns the most recently issued credential of hostID,
// revoked ones included: rotating a host whose credential was revoked is a
// legitimate way to bring it back without re-enrolling it.
func (s *Store) latestTokenOf(ctx context.Context, hostID string) (transport.TokenInfo, error) {
	var (
		info      transport.TokenInfo
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, host_id, label, created_at
		FROM ingest_tokens
		WHERE host_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, hostID).Scan(&info.ID, &info.HostID, &info.Label, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return transport.TokenInfo{}, nil
	}
	if err != nil {
		return transport.TokenInfo{}, fmt.Errorf("reading current token of host %s: %w", hostID, err)
	}
	info.CreatedAt = time.UnixMilli(createdAt).UTC()
	return info, nil
}
