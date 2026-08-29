package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

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

// runConformanceTests exercises the Relational contract itself, so every
// backend is held to exactly the same behavior (ADR-0003: "misma
// interfaz... cobertura en CI equivalente"). newStore must return a fresh,
// empty store — sqlite_test.go and postgres_test.go each supply their own
// way of getting one.
func runConformanceTests(t *testing.T, newStore func(t *testing.T) Relational) {
	t.Run("InsertAndListEvents", func(t *testing.T) {
		s := newStore(t)
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
	})

	t.Run("InsertSameIDTwiceIsNoOp", func(t *testing.T) {
		s := newStore(t)
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
	})

	t.Run("ListEventsFiltersByHost", func(t *testing.T) {
		s := newStore(t)
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
	})

	t.Run("SearchEventTitlesFindsMatch", func(t *testing.T) {
		s := newStore(t)
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
			t.Fatalf("expected exactly the segfault event via full-text match, got %+v", got)
		}
	})

	t.Run("InsertRejectsInvalidEvent", func(t *testing.T) {
		s := newStore(t)
		invalid := schema.Event{ID: "bad"} // missing host_id, ts, etc.
		if err := s.InsertEvent(context.Background(), invalid); err == nil {
			t.Fatal("expected an invalid event to be rejected before it touches storage")
		}
	})

	t.Run("ConcurrentInsertsAllLand", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)

		const n = 50
		errCh := make(chan error, n)
		for i := 0; i < n; i++ {
			i := i
			go func() {
				errCh <- s.InsertEvent(ctx, sampleEvent(fmt.Sprintf("evt-%d", i), ts, "host-a"))
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
	})
}
