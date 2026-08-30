package storage

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("unexpected error closing store: %v", err)
		}
	})
	return s
}

// TestSQLiteStore_Conformance holds SQLite to the same Relational contract
// PostgresStore is held to (ADR-0003).
func TestSQLiteStore_Conformance(t *testing.T) {
	runConformanceTests(t, func(t *testing.T) Relational {
		return newTestStore(t)
	})
}

// TestSQLiteStore_ListEventsAcrossMonthsUsesAttach is SQLite-specific: it
// exercises the monthly-file-per-database implementation detail (ATTACH
// DATABASE), which isn't part of the Relational contract itself — nothing
// says PostgresStore must partition by month the same way.
func TestSQLiteStore_ListEventsAcrossMonthsUsesAttach(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	july := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	august := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	if err := s.InsertEvent(ctx, sampleEvent("evt-july", july, "host-a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.InsertEvent(ctx, sampleEvent("evt-august", august, "host-a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := s.ListEvents(ctx, july.Add(-time.Hour), august.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("unexpected error listing across months: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events spanning July and August, got %d", len(got))
	}
	if got[0].ID != "evt-july" || got[1].ID != "evt-august" {
		t.Fatalf("expected chronological order july-then-august, got %+v", got)
	}
}
