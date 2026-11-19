package sqlitetokenstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// CreateEnrollmentCode implements transport.EnrollmentStore.
//
// The code is hashed with the same Argon2id parameters as an ingest token,
// and for the same reason: a copy of tokens.db must not contain anything
// that can be presented to the hub. It also means the hub physically cannot
// show a code twice, which is what makes "shown once" a property of the
// design rather than a promise in the UI.
func (s *Store) CreateEnrollmentCode(ctx context.Context, plaintextCode string, code transport.EnrollmentCode) error {
	if plaintextCode == "" {
		return errors.New("refusing to store an empty enrollment code")
	}
	if code.ExpiresAt.IsZero() {
		return errors.New("an enrollment code must have an expiry")
	}

	hash, err := transport.HashToken(plaintextCode)
	if err != nil {
		return fmt.Errorf("hashing enrollment code: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO enrollment_codes (code_hash, label, expected_host_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, hash, code.Label, code.ExpectedHostID, s.clock().UTC().UnixMilli(), code.ExpiresAt.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("storing enrollment code: %w", err)
	}
	return nil
}

// RedeemEnrollmentCode implements transport.EnrollmentStore.
//
// Single use is enforced by the database, not by the scan: the UPDATE that
// consumes the row requires consumed_at to still be NULL, so two requests
// arriving with the same valid code race to that statement and exactly one
// of them wins. Deciding it in Go, between the SELECT and the UPDATE, would
// let both callers walk away with an ingest token for the same enrollment.
func (s *Store) RedeemEnrollmentCode(ctx context.Context, plaintextCode string) (transport.EnrollmentCode, bool, error) {
	if plaintextCode == "" {
		return transport.EnrollmentCode{}, false, nil
	}
	now := s.clock().UTC()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code_hash, label, expected_host_id, expires_at
		FROM enrollment_codes
		WHERE consumed_at IS NULL
		  AND expires_at > ?
		ORDER BY id ASC
	`, now.UnixMilli())
	if err != nil {
		return transport.EnrollmentCode{}, false, fmt.Errorf("querying enrollment codes: %w", err)
	}

	type candidate struct {
		id   int64
		code transport.EnrollmentCode
	}
	var matched *candidate
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			rows.Close()
			return transport.EnrollmentCode{}, false, err
		}
		var (
			c         candidate
			hash      string
			expiresAt int64
		)
		if err := rows.Scan(&c.id, &hash, &c.code.Label, &c.code.ExpectedHostID, &expiresAt); err != nil {
			rows.Close()
			return transport.EnrollmentCode{}, false, fmt.Errorf("scanning enrollment code: %w", err)
		}
		c.code.ExpiresAt = time.UnixMilli(expiresAt).UTC()

		ok, err := transport.VerifyToken(plaintextCode, hash)
		if err != nil {
			// A malformed stored hash is one unusable row, not a reason to
			// abandon the rest of the scan.
			continue
		}
		if ok {
			matched = &c
			break
		}
	}
	iterErr := rows.Err()
	rows.Close()
	if iterErr != nil {
		return transport.EnrollmentCode{}, false, fmt.Errorf("iterating enrollment codes: %w", iterErr)
	}
	if matched == nil {
		return transport.EnrollmentCode{}, false, nil
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE enrollment_codes
		SET consumed_at = ?
		WHERE id = ? AND consumed_at IS NULL
	`, now.UnixMilli(), matched.id)
	if err != nil {
		return transport.EnrollmentCode{}, false, fmt.Errorf("consuming enrollment code: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return transport.EnrollmentCode{}, false, fmt.Errorf("confirming enrollment code consumption: %w", err)
	}
	if affected == 0 {
		// Another request consumed it between the scan and this UPDATE.
		// Indistinguishable, on purpose, from a code that never existed.
		return transport.EnrollmentCode{}, false, nil
	}
	return matched.code, true, nil
}

// PendingEnrollmentCodes reports how many codes are still redeemable. It
// exists for `bita agent list`, which should show that an enrollment was
// started and never finished — the one state no token row records.
func (s *Store) PendingEnrollmentCodes(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM enrollment_codes
		WHERE consumed_at IS NULL AND expires_at > ?
	`, s.clock().UTC().UnixMilli()).Scan(&count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("counting pending enrollment codes: %w", err)
	}
	return count, nil
}
