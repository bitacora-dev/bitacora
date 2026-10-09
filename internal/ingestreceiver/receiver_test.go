package ingestreceiver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/storage"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

type metricAppenderFunc func(context.Context, schema.Metric) error

func (f metricAppenderFunc) Append(ctx context.Context, metric schema.Metric) error {
	return f(ctx, metric)
}

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

func validInventory(hostID string, ts time.Time) *bitacorapb.Inventory {
	return &bitacorapb.Inventory{
		HostId:       hostID,
		Kind:         string(schema.InventoryDisk),
		ReportedAtMs: ts.UnixMilli(),
		Schema:       1,
		Items: []*bitacorapb.InventoryItem{{
			Id:    "/mnt/disk1",
			Name:  "disk1",
			Attrs: map[string]string{"health": "passed", "used_bytes": "42"},
		}},
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

func TestReceiveBatch_WritesInventory(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), events, logs, WithInventoryUpserter(events))
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{BatchId: "b1", HostId: "host-a", Inventories: []*bitacorapb.Inventory{validInventory("host-a", ts)}}
	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok, err := events.GetInventory(context.Background(), "host-a", schema.InventoryDisk)
	if err != nil || !ok {
		t.Fatalf("getting inventory: ok=%v err=%v", ok, err)
	}
	if !got.ReportedAt.Equal(ts) || len(got.Items) != 1 || got.Items[0].Attrs["health"] != "passed" {
		t.Errorf("inventory did not round-trip: %+v", got)
	}
}

func TestReceiveBatch_MixedBatchWritesEveryType(t *testing.T) {
	ms := newMetricStore(t)
	events := newRelationalStore(t)
	logs, dir := newLogStore(t)
	r := New(ms, events, logs, WithInventoryUpserter(events))
	ts := time.Now().UTC().Truncate(time.Millisecond)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("bitacora_cpu_usage_ratio", "host-a", ts)},
		Events:  []*bitacorapb.Event{validEvent("evt-1", "host-a", ts)},
		LogLines: []*bitacorapb.LogLine{
			validLogLine("host-a", ts),
		},
		Inventories: []*bitacorapb.Inventory{validInventory("host-a", ts)},
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
	if got, ok, err := events.GetInventory(context.Background(), "host-a", schema.InventoryDisk); err != nil || !ok || len(got.Items) != 1 {
		t.Errorf("expected 1 inventory item, got %+v (ok=%v err=%v)", got, ok, err)
	}
}

func TestReceiveBatch_InvalidMetricRecordsRejectionAndPersistsValidMetric(t *testing.T) {
	ms := newMetricStore(t)
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(ms, events, logs)
	ts := time.Now().UTC().Truncate(time.Millisecond)

	goodMetric := validMetric("bitacora_cpu_usage_ratio", "host-a", ts)
	badMetric := validMetric("not_a_valid_metric_name", "host-a", ts) // missing bitacora_ prefix
	goodEvent := validEvent("evt-good", "host-a", ts)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{badMetric, goodMetric},
		Events:  []*bitacorapb.Event{goodEvent},
	}

	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("a malformed item must not fail the whole batch, got err: %v", err)
	}

	gotMetrics, err := ms.Query(context.Background(), "bitacora_cpu_usage_ratio", ts.Add(-time.Minute), ts.Add(time.Minute))
	if err != nil || len(gotMetrics) != 1 {
		t.Errorf("expected the good metric to survive, got %d samples (err=%v)", len(gotMetrics), err)
	}

	gotEvents, err := events.ListEvents(context.Background(), ts.Add(-time.Minute), time.Now().UTC().Add(time.Minute), "host-a")
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(gotEvents) != 2 {
		t.Fatalf("expected valid event and validation rejection, got %+v", gotEvents)
	}
	var rejection *schema.Event
	for i := range gotEvents {
		if gotEvents[i].Type == "ingest.validation_rejected" {
			rejection = &gotEvents[i]
		}
	}
	if rejection == nil {
		t.Fatal("expected an ADR-0006 validation rejection event")
	}
	if rejection.HostID != "host-a" || rejection.Attrs["data_kind"] != "metric" || rejection.Attrs["data_name"] != "not_a_valid_metric_name" || rejection.Attrs["reason"] == "" {
		t.Errorf("unexpected validation rejection event: %+v", rejection)
	}
}

func TestReceiveBatch_ValidationRejectionsAreRateLimited(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), events, logs)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("not_a_valid_metric_name", "host-a", now)},
	}
	for i := 0; i < 2; i++ {
		if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
			t.Fatalf("receiving rejected batch %d: %v", i, err)
		}
	}

	listRejections := func() []schema.Event {
		t.Helper()
		got, err := events.ListEvents(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), "host-a")
		if err != nil {
			t.Fatalf("listing rejection events: %v", err)
		}
		return got
	}
	if got := listRejections(); len(got) != 1 {
		t.Fatalf("expected one rejection inside rate limit, got %+v", got)
	}

	now = now.Add(validationRejectionInterval)
	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("receiving batch after rate limit: %v", err)
	}
	if got := listRejections(); len(got) != 2 {
		t.Fatalf("expected a second rejection after rate limit, got %+v", got)
	}
}

func TestReceiveBatch_ValidationRejectionsKeepDistinctDataNames(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), events, logs)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{
			validMetric("not_a_valid_metric_name", "host-a", now),
			validMetric("another_invalid_metric_name", "host-a", now),
		},
	}
	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("receiving rejected batch: %v", err)
	}

	got, err := events.ListEvents(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("listing rejection events: %v", err)
	}
	seen := make(map[string]bool)
	for _, event := range got {
		if event.Type == "ingest.validation_rejected" {
			seen[event.Attrs["data_name"]] = true
		}
	}
	for _, name := range []string{"not_a_valid_metric_name", "another_invalid_metric_name"} {
		if !seen[name] {
			t.Errorf("expected a validation rejection for %q, got %+v", name, got)
		}
	}
}

func TestReceiveBatch_RecordsValidationRejectionsForEveryDataKind(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(newMetricStore(t), events, logs, WithInventoryUpserter(events), WithJobInserter(events))
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("not_a_valid_metric_name", "host-a", now)},
		Events: []*bitacorapb.Event{{
			Id: "bad-event", HostId: "host-a", TsMs: now.UnixMilli(), Source: "kernel", Type: "kernel.segfault", Severity: "invalid", Title: "bad", Schema: 1,
		}},
		LogLines:    []*bitacorapb.LogLine{{HostId: "host-a", TsMs: now.UnixMilli()}},
		Inventories: []*bitacorapb.Inventory{{HostId: "host-a", ReportedAtMs: now.UnixMilli(), Schema: 1}},
		Jobs:        []*bitacorapb.Job{{Id: "bad-job", HostId: "host-a"}},
	}
	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("rejected items must not fail the batch: %v", err)
	}

	got, err := events.ListEvents(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("listing validation rejection events: %v", err)
	}
	seen := make(map[string]bool)
	for _, event := range got {
		if event.Type == "ingest.validation_rejected" {
			seen[event.Attrs["data_kind"]] = true
		}
	}
	for _, dataKind := range []string{"metric", "event", "log", "inventory", "job"} {
		if !seen[dataKind] {
			t.Errorf("expected validation rejection for %s, got %+v", dataKind, got)
		}
	}
}

func TestReceiveBatch_BackendErrorsAreNotValidationRejections(t *testing.T) {
	events := newRelationalStore(t)
	logs, _ := newLogStore(t)
	r := New(metricAppenderFunc(func(context.Context, schema.Metric) error {
		return errors.New("metric store unavailable")
	}), events, logs)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	batch := &bitacorapb.Batch{
		BatchId: "b1",
		HostId:  "host-a",
		Metrics: []*bitacorapb.Metric{validMetric("bitacora_cpu_usage_ratio", "host-a", now)},
	}
	if err := r.ReceiveBatch(context.Background(), "host-a", batch); err != nil {
		t.Fatalf("backend error must not fail the batch: %v", err)
	}

	got, err := events.ListEvents(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), "host-a")
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("backend error must not be recorded as a validation rejection: %+v", got)
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
