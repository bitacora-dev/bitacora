package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
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

func sampleEvent(id string, ts time.Time, hostID string) schema.Event {
	return schema.Event{
		ID:       id,
		TS:       ts,
		HostID:   hostID,
		Source:   "kernel",
		Type:     "kernel.segfault",
		Severity: schema.SeverityError,
		Title:    "segfault in node (cpu 8)",
		Schema:   schema.CurrentSchemaVersion,
	}
}

func TestSQLiteStore_InsertAndListEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)
	e := sampleEvent("evt-1", ts, "host-a")

	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatalf("unexpected error inserting event: %v", err)
	}

	got, err := s.ListEvents(ctx, ts.Add(-time.Hour), ts.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("unexpected error listing events: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].ID != e.ID || got[0].Title != e.Title || !got[0].TS.Equal(ts) {
		t.Fatalf("round-tripped event doesn't match: got %+v", got[0])
	}
}

func TestSQLiteStore_InsertSameIDTwiceIsNoOp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)
	e := sampleEvent("evt-dup", ts, "host-a")

	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatalf("unexpected error on first insert: %v", err)
	}
	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatalf("unexpected error on duplicate insert: %v", err)
	}

	got, err := s.ListEvents(ctx, ts.Add(-time.Hour), ts.Add(time.Hour), "")
	if err != nil {
		t.Fatalf("unexpected error listing events: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 event after a duplicate insert, got %d", len(got))
	}
}

func TestSQLiteStore_ListEventsFiltersByHost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)

	if err := s.InsertEvent(ctx, sampleEvent("evt-a", ts, "host-a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.InsertEvent(ctx, sampleEvent("evt-b", ts, "host-b")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := s.ListEvents(ctx, ts.Add(-time.Hour), ts.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "evt-a" {
		t.Fatalf("expected only host-a's event, got %+v", got)
	}
}

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

func TestSQLiteStore_SearchEventTitlesUsesFTS5(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)

	segfault := sampleEvent("evt-segfault", ts, "host-a")
	segfault.Title = "segfault in node (cpu 8)"

	oom := sampleEvent("evt-oom", ts, "host-a")
	oom.Type = "kernel.oom"
	oom.Title = "out of memory killer invoked"

	if err := s.InsertEvent(ctx, segfault); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.InsertEvent(ctx, oom); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := s.SearchEventTitles(ctx, "segfault", 10)
	if err != nil {
		t.Fatalf("unexpected error searching: %v", err)
	}
	if len(got) != 1 || got[0].ID != "evt-segfault" {
		t.Fatalf("expected exactly the segfault event via FTS5 match, got %+v", got)
	}
}

func TestSQLiteStore_InsertRejectsInvalidEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	invalid := schema.Event{ID: "bad"} // missing host_id, ts, etc.
	if err := s.InsertEvent(ctx, invalid); err == nil {
		t.Fatal("expected an invalid event to be rejected before it touches storage")
	}
}

func TestSQLiteStore_WritesFromManyGoroutinesAreSerializedNotLost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)

	const n = 50
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			e := sampleEvent(fmt.Sprintf("evt-%d", i), ts, "host-a")
			errCh <- s.InsertEvent(ctx, e)
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("unexpected error from concurrent insert: %v", err)
		}
	}

	got, err := s.ListEvents(ctx, ts.Add(-time.Hour), ts.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("unexpected error listing events: %v", err)
	}
	if len(got) != n {
		t.Fatalf("expected all %d concurrently written events to land, got %d", n, len(got))
	}
}
