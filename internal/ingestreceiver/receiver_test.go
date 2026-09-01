package ingestreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/storage"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// Receiver must satisfy transport.BatchReceiver: that's the whole point of
// this package.
var _ transport.BatchReceiver = (*Receiver)(nil)

func newMetricStore(t *testing.T) *metricstore.Store {
	t.Helper()
	s, err := metricstore.Open(t.TempDir(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("opening metricstore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("closing metricstore: %v", err)
		}
	})
	return s
}

func newRelationalStore(t *testing.T) storage.Relational {
	t.Helper()
	s, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("opening sqlite store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("closing sqlite store: %v", err)
		}
	})
	return s
}

// newLogStore flushes every log line as it's appended (one-byte
// threshold), so tests can read blocks back immediately instead of racing
// the size/age buffering logstore.Store normally does. It returns the
// base dir too, since Store.Append flushes synchronously here and
// discards the returned BlockMeta — tests use logstore.ScanIndex(dir) to
// see what actually landed on disk.
func newLogStore(t *testing.T) (*logstore.Store, string) {
	t.Helper()
	dir := t.TempDir()
	return logstore.NewStore(dir, logstore.WithLimits(1, logstore.DefaultMaxAge)), dir
}

func validMetric(name, hostID string, ts time.Time) *bitacorapb.Metric {
	return &bitacorapb.Metric{
		Name:        name,
		HostId:      hostID,
		Value:       0.42,
		TimestampMs: ts.UnixMilli(),
	}
}

func validEvent(id, hostID string, ts time.Time) *bitacorapb.Event {
	return &bitacorapb.Event{
		Id:       id,
		TsMs:     ts.UnixMilli(),
		HostId:   hostID,
		Source:   "kernel",
		Type:     "kernel.segfault",
		Severity: "error",
		Title:    "segfault in node (cpu 8)",
		Schema:   1,
		Subject:  &bitacorapb.EventSubject{Kind: "process", Name: "node"},
	}
}

func validLogLine(hostID string, ts time.Time) *bitacorapb.LogLine {
	return &bitacorapb.LogLine{
		TsMs:    ts.UnixMilli(),
		HostId:  hostID,
		Source:  "journald",
		Message: "hello from the agent",
	}
}

func TestReceiveBatch_WritesMetric(t *testing.T) {
	ms := newMetricStore(t)
	logs, _ := newLogStore(t)
	r := New(ms, newRelationalStore(t), logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("bitacora_cpu_usage_ratio", "host-a", ts)},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := ms.Query(context.Background(), "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil {
		t.Fatalf("querying metricstore: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if got[0].Value != 0.42 {
		t.Errorf("expected value 0.42, got %v", got[0].Value)
	}
}

func TestReceiveBatch_WritesEvent(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), events, logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Events:  []*bitacorapb.Event{validEvent("evt-1", "host-a", ts)},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := events.ListEvents(context.Background(), ts.Add(-time.Minute), ts.Add(time.Minute), "host-a")
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].ID != "evt-1" || got[0].Title != "segfault in node (cpu 8)" || got[0].Subject.Name != "node" {
		t.Errorf("round-tripped event doesn't match: %+v", got[0])
	}
}

func TestReceiveBatch_WritesLogLine(t *testing.T) {
	logs, dir := newLogStore(t)
	r := New(newMetricStore(t), newRelationalStore(t), logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{
		BatchId:  "b1",
		HostId:   "host-a",
		LogLines: []*bitacorapb.LogLine{validLogLine("host-a", ts)},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := logstore.ScanIndex(dir)
	if err != nil {
		t.Fatalf("scanning log store index: %v", err)
	}
	if len(result.Blocks) != 1 {
		t.Fatalf("expected 1 block on disk, got %d", len(result.Blocks))
	}
	if result.Blocks[0].HostID != "host-a" || result.Blocks[0].NLines != 1 {
		t.Errorf("unexpected block meta: %+v", result.Blocks[0])
	}
}

func TestReceiveBatch_MixedBatchWritesEveryType(t *testing.T) {
	ms := newMetricStore(t)
	events := newRelationalStore(t)
	logs, dir := newLogStore(t)
	r := New(ms, events, logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("bitacora_cpu_usage_ratio", "host-a", ts)},
		Events:  []*bitacorapb.Event{validEvent("evt-1", "host-a", ts)},
		LogLines: []*bitacorapb.LogLine{
			validLogLine("host-a", ts),
		},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, err := ms.Query(context.Background(), "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute)); err != nil || len(got) != 1 {
		t.Errorf("expected 1 metric sample, got %d (err=%v)", len(got), err)
	}
	if got, err := events.ListEvents(context.Background(), ts.Add(-time.Minute), ts.Add(time.Minute), "host-a"); err != nil || len(got) != 1 {
		t.Errorf("expected 1 event, got %d (err=%v)", len(got), err)
	}
	if result, err := logstore.ScanIndex(dir); err != nil || len(result.Blocks) != 1 {
		t.Errorf("expected 1 log block on disk, got %d (err=%v)", len(result.Blocks), err)
	}
}

func TestReceiveBatch_MalformedItemDoesNotBlockTheRest(t *testing.T) {
	ms := newMetricStore(t)
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(ms, events, logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	goodMetric := validMetric("bitacora_cpu_usage_ratio", "host-a", ts)
	badMetric := validMetric("not_a_valid_metric_name", "host-a", ts) // missing bitacora_ prefix
	goodEvent := validEvent("evt-good", "host-a", ts)
	badEvent := validEvent("evt-bad", "host-a", ts)
	badEvent.Severity = "not-a-real-severity"

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{badMetric, goodMetric},
		Events:  []*bitacorapb.Event{badEvent, goodEvent},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("a malformed item must not fail the whole batch, got err: %v", err)
	}

	gotMetrics, err := ms.Query(context.Background(), "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil || len(gotMetrics) != 1 {
		t.Errorf("expected the good metric to survive, got %d samples (err=%v)", len(gotMetrics), err)
	}

	gotEvents, err := events.ListEvents(context.Background(), ts.Add(-time.Minute), ts.Add(time.Minute), "host-a")
	if err != nil || len(gotEvents) != 1 || gotEvents[0].ID != "evt-good" {
		t.Errorf("expected only the good event to survive, got %+v (err=%v)", gotEvents, err)
	}
}

func TestReceiveBatch_EmptyBatchIsNotAnError(t *testing.T) {
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), newRelationalStore(t), logs)

	batch := &bitacorapb.Batch{BatchId: "b1", HostId: "host-a"}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error on empty batch: %v", err)
	}
}

func TestReceiveBatch_CanceledContextFailsFast(t *testing.T) {
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), newRelationalStore(t), logs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	batch := &bitacorapb.Batch{BatchId: "b1", HostId: "host-a", Metrics: []*bitacorapb.Metric{validMetric("bitacora_cpu_usage_ratio", "host-a", time.Now())}}

	if err := r.ReceiveBatch(ctx, "host-a", batch); err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
}
