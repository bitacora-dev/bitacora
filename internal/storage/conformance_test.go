package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func sampleInventory(hostID string, kind schema.InventoryKind, reportedAt time.Time) schema.Inventory {
	return schema.Inventory{
		HostID:     hostID,
		Kind:       kind,
		ReportedAt: reportedAt,
		Schema:     schema.CurrentSchemaVersion,
		Items: []schema.InventoryItem{
			{ID: "multimedia", Name: "Multimedia", Attrs: schema.Labels{"path": "/mnt/user/multimedia", "protocol": "smb"}},
		},
	}
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

	t.Run("UpsertAndGetInventory", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ts := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)

		inv := sampleInventory("host-a", schema.InventoryShare, ts)
		if err := s.UpsertInventory(ctx, inv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, ok, err := s.GetInventory(ctx, "host-a", schema.InventoryShare)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("expected the inventory to be found")
		}
		if got.HostID != inv.HostID || got.Kind != inv.Kind || !got.ReportedAt.Equal(inv.ReportedAt) {
			t.Fatalf("round-tripped inventory doesn't match: got %+v, want %+v", got, inv)
		}
		if len(got.Items) != 1 || got.Items[0].ID != "multimedia" || got.Items[0].Attrs["path"] != "/mnt/user/multimedia" {
			t.Fatalf("unexpected items: %+v", got.Items)
		}
	})

	t.Run("UpsertInventoryReplacesPreviousSnapshot", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ts := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)

		first := sampleInventory("host-a", schema.InventoryShare, ts)
		if err := s.UpsertInventory(ctx, first); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		second := first
		second.ReportedAt = ts.Add(time.Hour)
		second.Items = []schema.InventoryItem{{ID: "isos", Name: "ISOs"}}
		if err := s.UpsertInventory(ctx, second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, ok, err := s.GetInventory(ctx, "host-a", schema.InventoryShare)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("expected the inventory to be found")
		}
		if len(got.Items) != 1 || got.Items[0].ID != "isos" {
			t.Fatalf("expected the snapshot to have been fully replaced, got %+v", got.Items)
		}
		if !got.ReportedAt.Equal(second.ReportedAt) {
			t.Fatalf("expected reported_at to reflect the newer snapshot, got %v", got.ReportedAt)
		}
	})

	t.Run("DifferentKindsForSameHostAreIndependent", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ts := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)

		shares := sampleInventory("host-a", schema.InventoryShare, ts)
		vms := sampleInventory("host-a", schema.InventoryVM, ts)
		vms.Items = []schema.InventoryItem{{ID: "plex", Name: "plex"}}

		if err := s.UpsertInventory(ctx, shares); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := s.UpsertInventory(ctx, vms); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gotShares, ok, err := s.GetInventory(ctx, "host-a", schema.InventoryShare)
		if err != nil || !ok {
			t.Fatalf("unexpected: ok=%v err=%v", ok, err)
		}
		if gotShares.Items[0].ID != "multimedia" {
			t.Fatalf("expected the share inventory untouched, got %+v", gotShares.Items)
		}

		gotVMs, ok, err := s.GetInventory(ctx, "host-a", schema.InventoryVM)
		if err != nil || !ok {
			t.Fatalf("unexpected: ok=%v err=%v", ok, err)
		}
		if gotVMs.Items[0].ID != "plex" {
			t.Fatalf("expected the vm inventory independent of shares, got %+v", gotVMs.Items)
		}
	})

	t.Run("GetInventoryNotFound", func(t *testing.T) {
		s := newStore(t)
		_, ok, err := s.GetInventory(context.Background(), "host-nope", schema.InventoryShare)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Fatal("expected ok=false for a host/kind that was never reported")
		}
	})

	t.Run("UpsertInventoryRejectsInvalid", func(t *testing.T) {
		s := newStore(t)
		invalid := schema.Inventory{HostID: "host-a"} // missing kind, reported_at, schema
		if err := s.UpsertInventory(context.Background(), invalid); err == nil {
			t.Fatal("expected an invalid inventory to be rejected before it touches storage")
		}
	})
}
