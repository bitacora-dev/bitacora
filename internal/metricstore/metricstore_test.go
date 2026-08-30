package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), 7*24*time.Hour)
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

func TestStore_AppendAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)

	m := schema.Metric{
		Name:      "bitacora_cpu_usage_ratio",
		HostID:    "host-a",
		Labels:    schema.Labels{"cpu": "0"},
		Value:     0.42,
		Timestamp: ts,
	}
	if err := s.Append(ctx, m); err != nil {
		t.Fatalf("unexpected error appending: %v", err)
	}

	got, err := s.Query(ctx, "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error querying: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if got[0].Value != 0.42 || got[0].Labels["host_id"] != "host-a" || got[0].Labels["cpu"] != "0" {
		t.Fatalf("unexpected sample: %+v", got[0])
	}
}

func TestStore_QueryFiltersByExtraMatcher(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)

	for _, host := range []string{"host-a", "host-b"} {
		m := schema.Metric{Name: "bitacora_cpu_usage_ratio", HostID: host, Value: 0.1, Timestamp: ts}
		if err := s.Append(ctx, m); err != nil {
			t.Fatalf("unexpected error appending: %v", err)
		}
	}

	matcher := labels.MustNewMatcher(labels.MatchEqual, "host_id", "host-a")
	got, err := s.Query(ctx, "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute), matcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Labels["host_id"] != "host-a" {
		t.Fatalf("expected only host-a's sample, got %+v", got)
	}
}

func TestStore_AppendRejectsInvalidMetric(t *testing.T) {
	s := newTestStore(t)
	invalid := schema.Metric{Name: "not_prefixed_bytes"} // missing bitacora_ prefix, host_id, timestamp
	if err := s.Append(context.Background(), invalid); err == nil {
		t.Fatal("expected an invalid metric to be rejected before it touches storage")
	}
}
