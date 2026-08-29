package storage

import (
	"os"
	"testing"
)

// postgresDSN skips the test (not fails) when no PostgreSQL is available.
// CI sets BITACORA_TEST_POSTGRES_DSN against a real service container, per
// ADR-0003's "CI ejecuta la misma suite de integración contra SQLite y
// PostgreSQL, ambas en verde" — that's an environment CI provides, not a
// dependency every contributor's laptop needs.
func postgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("BITACORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BITACORA_TEST_POSTGRES_DSN not set; skipping PostgreSQL conformance tests")
	}
	return dsn
}

// TestPostgresStore_Conformance holds PostgreSQL to exactly the same
// Relational contract SQLite is held to (ADR-0003).
func TestPostgresStore_Conformance(t *testing.T) {
	dsn := postgresDSN(t)

	runConformanceTests(t, func(t *testing.T) Relational {
		s, err := NewPostgresStore(dsn)
		if err != nil {
			t.Fatalf("unexpected error opening postgres store: %v", err)
		}
		t.Cleanup(func() {
			// Each subtest expects a fresh store; the database itself is
			// shared and persistent (unlike SQLite's per-test temp dir), so
			// clear it between subtests instead of between whole test runs.
			if _, err := s.db.Exec("TRUNCATE events, inventories"); err != nil {
				t.Errorf("unexpected error truncating tables: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Errorf("unexpected error closing store: %v", err)
			}
		})
		return s
	})
}

func TestPostgresStore_MigratesCleanlyTwice(t *testing.T) {
	dsn := postgresDSN(t)

	s1, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("unexpected error on first open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}

	// Re-opening and re-migrating against an already-migrated database
	// must be a no-op, not an error — CREATE TABLE/INDEX IF NOT EXISTS.
	s2, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("unexpected error on second open (re-migration): %v", err)
	}
	if _, err := s2.db.Exec("TRUNCATE events"); err != nil {
		t.Errorf("unexpected error truncating events: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}
}
