// Package ingestreceiver implements transport.BatchReceiver: it takes a
// bitacorapb.Batch — already authenticated, dedup-checked and decoded by
// internal/transport per ADR-0008 — and writes each item to its backend
// (metricstore, the relational event store, logstore).
//
// batch.inventories isn't handled here: there is no bitacorapb.Inventory
// message in proto/ingest.proto yet, no schema.Inventory type, and no
// storage.Relational.UpsertInventory method — ADR-0015/0016/0017 describe
// the shape but it hasn't landed in code. Wiring it in is a follow-up once
// those exist; this package only handles what the wire format actually
// carries today (metrics, events, log lines).
package ingestreceiver

import (
	"context"
	"log/slog"

	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// MetricAppender is the write side of a metricstore.Store that Receiver
// needs, narrowed the same way hubapi.MetricQuerier narrows the read side:
// so this package doesn't depend on the concrete type and is testable with
// a fake. *metricstore.Store satisfies this without any change on its end.
type MetricAppender interface {
	Append(ctx context.Context, m schema.Metric) error
}

// EventInserter is the write side of storage.Relational that Receiver
// needs. *storage.SQLiteStore (and any other Relational backend) satisfies
// this already.
type EventInserter interface {
	InsertEvent(ctx context.Context, e schema.Event) error
}

// LogAppender is the write side of a logstore.Store that Receiver needs.
// *logstore.Store satisfies this without any change on its end.
type LogAppender interface {
	Append(line schema.LogLine) (*logstore.BlockMeta, error)
}

// Receiver implements transport.BatchReceiver against real storage
// backends.
type Receiver struct {
	Metrics MetricAppender
	Events  EventInserter
	Logs    LogAppender
}

// New returns a Receiver writing to the given backends.
func New(metrics MetricAppender, events EventInserter, logs LogAppender) *Receiver {
	return &Receiver{Metrics: metrics, Events: events, Logs: logs}
}

// ReceiveBatch writes every item in batch to its backend and never fails
// the batch over a single bad item: a malformed or rejected metric, event
// or log line is logged and skipped, and the rest of the batch is still
// written (ADR-0008: the hub must stay reliable in the face of odd data).
//
// This is deliberate, not just convenient: internal/transport.Server marks
// a batch_id as seen *before* calling ReceiveBatch, so if this returned an
// error the agent's retry would find the batch already marked as
// duplicate and it would never be re-delivered — the failed items would be
// lost for good rather than retried. Returning an error here would make
// things worse, not safer, so ReceiveBatch only reports the batch as
// failed when it couldn't attempt to write anything at all (ctx already
// canceled).
func (r *Receiver) ReceiveBatch(ctx context.Context, hostID string, batch *bitacorapb.Batch) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	batchID := batch.GetBatchId()

	for _, m := range batch.GetMetrics() {
		metric := protoToMetric(m)
		if err := r.Metrics.Append(ctx, metric); err != nil {
			slog.Error("ingestreceiver: dropping metric", "host_id", hostID, "batch_id", batchID, "name", m.GetName(), "err", err)
		}
	}

	for _, e := range batch.GetEvents() {
		event := protoToEvent(e)
		if err := r.Events.InsertEvent(ctx, event); err != nil {
			slog.Error("ingestreceiver: dropping event", "host_id", hostID, "batch_id", batchID, "event_id", e.GetId(), "err", err)
		}
	}

	for _, l := range batch.GetLogLines() {
		line := protoToLogLine(l)
		if _, err := r.Logs.Append(line); err != nil {
			slog.Error("ingestreceiver: dropping log line", "host_id", hostID, "batch_id", batchID, "source", l.GetSource(), "err", err)
		}
	}

	return nil
}
