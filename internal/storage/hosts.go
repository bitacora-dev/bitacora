package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

var hostMigrations = []string{
	`CREATE TABLE IF NOT EXISTS hosts (
		id            TEXT PRIMARY KEY,
		name          TEXT NOT NULL DEFAULT '',
		hostname      TEXT NOT NULL DEFAULT '',
		agent_version TEXT NOT NULL DEFAULT '',
		last_seen_at  INTEGER
	)`,
	// Action tokens deliberately live apart from browser sessions, device
	// tokens and ingest tokens. Their hash is bound to exactly one human,
	// host, operation and request, and the used_at transition is durable.
	`CREATE TABLE IF NOT EXISTS action_tokens (
		id          TEXT PRIMARY KEY,
		request_id  TEXT NOT NULL UNIQUE,
		token_hash  TEXT NOT NULL,
		subject     TEXT NOT NULL,
		host_id     TEXT NOT NULL,
		operation   INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		used_at     INTEGER,
		created_at  INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS action_audit_events (
		id             TEXT PRIMARY KEY,
		request_id     TEXT NOT NULL,
		subject        TEXT NOT NULL,
		host_id        TEXT NOT NULL,
		operation      INTEGER NOT NULL,
		network_origin TEXT NOT NULL,
		result         TEXT NOT NULL,
		occurred_at    INTEGER NOT NULL
	)`,
	// Enforce audit immutability in the database as well as at the API
	// boundary. No update/delete API exists, and direct mutation is rejected.
	`CREATE TRIGGER IF NOT EXISTS action_audit_events_no_update
		BEFORE UPDATE ON action_audit_events
		BEGIN SELECT RAISE(ABORT, 'action audit is append-only'); END`,
	`CREATE TRIGGER IF NOT EXISTS action_audit_events_no_delete
		BEFORE DELETE ON action_audit_events
		BEGIN SELECT RAISE(ABORT, 'action audit is append-only'); END`,
	`CREATE TABLE IF NOT EXISTS pending_package_operations (
		request_id TEXT PRIMARY KEY,
		host_id    TEXT NOT NULL,
		operation  INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_pending_package_operations_host_expiry ON pending_package_operations(host_id, expires_at, created_at)`,
}

func scanHosts(rows *sql.Rows) ([]schema.Host, error) {
	defer rows.Close()
	var hosts []schema.Host
	for rows.Next() {
		var host schema.Host
		var lastSeenAt sql.NullInt64
		if err := rows.Scan(&host.ID, &host.Name, &host.Hostname, &host.AgentVersion, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scanning host: %w", err)
		}
		if lastSeenAt.Valid {
			host.LastSeenAt = time.UnixMilli(lastSeenAt.Int64).UTC()
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hosts: %w", err)
	}
	return hosts, nil
}

var _ actionconfirm.Backend = (*SQLiteStore)(nil)

// StoreActionToken persists only the Argon2id hash of a second-factor action
// token. It is intentionally unrelated to the other token tables.
func (s *SQLiteStore) StoreActionToken(ctx context.Context, token actionconfirm.TokenRecord) error {
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.hostDatabase()
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO action_tokens (id, request_id, token_hash, subject, host_id, operation, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, token.ID, token.RequestID, token.TokenHash, token.Subject, token.HostID, int32(token.Operation), token.ExpiresAt.UnixMilli(), token.CreatedAt.UnixMilli())
		if err != nil {
			return fmt.Errorf("storing action token: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) FindActionTokens(ctx context.Context, requestID string) ([]actionconfirm.TokenRecord, error) {
	db, err := s.hostDatabase()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, request_id, token_hash, subject, host_id, operation, expires_at, used_at, created_at
		FROM action_tokens WHERE request_id = ?
	`, requestID)
	if err != nil {
		return nil, fmt.Errorf("querying action tokens: %w", err)
	}
	defer rows.Close()
	var records []actionconfirm.TokenRecord
	for rows.Next() {
		var record actionconfirm.TokenRecord
		var operation int32
		var expiresAt, createdAt int64
		var usedAt sql.NullInt64
		if err := rows.Scan(&record.ID, &record.RequestID, &record.TokenHash, &record.Subject, &record.HostID, &operation, &expiresAt, &usedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning action token: %w", err)
		}
		record.Operation = bitacorapb.PackageOperation(operation)
		record.ExpiresAt = time.UnixMilli(expiresAt).UTC()
		record.CreatedAt = time.UnixMilli(createdAt).UTC()
		if usedAt.Valid {
			at := time.UnixMilli(usedAt.Int64).UTC()
			record.UsedAt = &at
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating action tokens: %w", err)
	}
	return records, nil
}

func (s *SQLiteStore) ConsumeActionToken(ctx context.Context, tokenID string, usedAt time.Time) (bool, error) {
	var consumed bool
	err := s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.hostDatabase()
		if err != nil {
			return err
		}
		result, err := db.ExecContext(ctx, `UPDATE action_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, usedAt.UnixMilli(), tokenID)
		if err != nil {
			return fmt.Errorf("consuming action token: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("checking consumed action token: %w", err)
		}
		consumed = count == 1
		return nil
	})
	return consumed, err
}

// AppendActionAudit is the only audit write. There is deliberately no matching
// update or delete method, and migrations add triggers as a second barrier.
func (s *SQLiteStore) AppendActionAudit(ctx context.Context, event actionconfirm.AuditEvent) error {
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.hostDatabase()
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO action_audit_events (id, request_id, subject, host_id, operation, network_origin, result, occurred_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, event.ID, event.RequestID, event.Subject, event.HostID, int32(event.Operation), event.NetworkOrigin, string(event.Result), event.OccurredAt.UnixMilli())
		if err != nil {
			return fmt.Errorf("appending action audit event: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) QueuePendingOrder(ctx context.Context, order actionconfirm.PendingOrder) error {
	return s.enqueueWrite(ctx, func(ctx context.Context) error {
		db, err := s.hostDatabase()
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO pending_package_operations (request_id, host_id, operation, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, order.RequestID, order.HostID, int32(order.Operation), order.ExpiresAt.UnixMilli(), order.CreatedAt.UnixMilli())
		if err != nil {
			return fmt.Errorf("queueing pending package operation: %w", err)
		}
		return nil
	})
}

// NextPendingOrder is deliberately a pull-only transport.PendingOrderSource:
// the hub never opens a command channel to an agent and only returns the
// closed protobuf operation after it was human-confirmed.
func (s *SQLiteStore) NextPendingOrder(ctx context.Context, hostID string, now time.Time) *bitacorapb.PendingPackageOperation {
	db, err := s.hostDatabase()
	if err != nil {
		return nil
	}
	var requestID string
	var operation int32
	var expiresAt int64
	err = db.QueryRowContext(ctx, `
		SELECT request_id, operation, expires_at
		FROM pending_package_operations
		WHERE host_id = ? AND expires_at > ?
		ORDER BY created_at ASC LIMIT 1
	`, hostID, now.UnixMilli()).Scan(&requestID, &operation, &expiresAt)
	if err != nil {
		return nil
	}
	return &bitacorapb.PendingPackageOperation{
		RequestId: requestID, Operation: bitacorapb.PackageOperation(operation), ExpiresAtMs: expiresAt,
	}
}
