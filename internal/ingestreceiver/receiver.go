// Package ingestreceiver implements transport.BatchReceiver: it takes a
// bitacorapb.Batch — already authenticated, dedup-checked and decoded by
// internal/transport per ADR-0008 — and writes each item to its backend
// (metricstore, the relational event store, logstore and Inventory store).
package ingestreceiver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

const validationRejectionInterval = 5 * time.Minute

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
type JobInserter interface {
	InsertJob(ctx context.Context, job schema.Job) error
}

// InventoryUpserter is the write side of storage.Relational that Receiver
// needs for declarative Inventory snapshots. Inventories replace the prior
// snapshot for their host and kind rather than appending history.
type InventoryUpserter interface {
	UpsertInventory(ctx context.Context, inv schema.Inventory) error
}

// LogAppender is the write side of a logstore.Store that Receiver needs.
// *logstore.Store satisfies this without any change on its end.
type LogAppender interface {
	Append(line schema.LogLine) (*logstore.BlockMeta, error)
}

// LogProcessor performs hub-side work after a raw log line has been
// persisted. It keeps extraction and alert evaluation out of agents while
// letting Receiver retain responsibility for transport durability ordering.
type LogProcessor interface {
	Process(ctx context.Context, line schema.LogLine) error
}

// Receiver implements transport.BatchReceiver against real storage
// backends.
type Receiver struct {
	Metrics     MetricAppender
	Events      EventInserter
	Jobs        JobInserter
	Inventories InventoryUpserter
	Logs        LogAppender
	Processor   LogProcessor

	now        func() time.Time
	rejections validationRejectionLimiter
}

// New returns a Receiver writing to the given backends.
func New(metrics MetricAppender, events EventInserter, logs LogAppender, options ...Option) *Receiver {
	r := &Receiver{Metrics: metrics, Events: events, Logs: logs, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(r)
	}
	return r
}

// validationRejectionLimiter bounds diagnostic events by host, data name, and
// stable reason. Rejected input must stay observable without allowing a broken
// agent to turn one malformed sample per batch into an event flood.
type validationRejectionLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *validationRejectionLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < validationRejectionInterval {
		return false
	}
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	l.last[key] = now
	return true
}

// Option configures an optional hub-side receive behavior.
type Option func(*Receiver)

// WithLogProcessor runs processor only after the raw LogLine has been
// appended successfully. Processor errors are handled like other malformed
// individual items: they never make an already accepted batch fail.
func WithLogProcessor(processor LogProcessor) Option {
	return func(r *Receiver) { r.Processor = processor }
}

// WithInventoryUpserter persists Inventory snapshots received from agents.
func WithInventoryUpserter(inventories InventoryUpserter) Option {
	return func(r *Receiver) { r.Inventories = inventories }
}
func WithJobInserter(jobs JobInserter) Option { return func(r *Receiver) { r.Jobs = jobs } }

// ReceiveBatch writes every item in batch to its backend and never fails
// the batch over a single bad item: a malformed or rejected metric, event
// log line or Inventory is logged and skipped, and the rest of the batch is still
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
		if err := metric.Validate(); err != nil {
			r.recordValidationRejection(ctx, hostID, "metric", m.GetName(), err)
			continue
		}
		if err := r.Metrics.Append(ctx, metric); err != nil {
			slog.Error("ingestreceiver: dropping metric", "host_id", hostID, "batch_id", batchID, "name", m.GetName(), "err", err)
		}
	}

	for _, e := range batch.GetEvents() {
		event := protoToEvent(e)
		if err := event.Validate(); err != nil {
			r.recordValidationRejection(ctx, hostID, "event", e.GetId(), err)
			continue
		}
		if err := r.Events.InsertEvent(ctx, event); err != nil {
			slog.Error("ingestreceiver: dropping event", "host_id", hostID, "batch_id", batchID, "event_id", e.GetId(), "err", err)
		}
	}

	for _, l := range batch.GetLogLines() {
		line := protoToLogLine(l)
		if err := line.Validate(); err != nil {
			r.recordValidationRejection(ctx, hostID, "log", l.GetSource(), err)
			continue
		}
		if _, err := r.Logs.Append(line); err != nil {
			slog.Error("ingestreceiver: dropping log line", "host_id", hostID, "batch_id", batchID, "source", l.GetSource(), "err", err)
			continue
		}
		if r.Processor != nil {
			if err := r.Processor.Process(ctx, line); err != nil {
				slog.Error("ingestreceiver: processing persisted log line", "host_id", hostID, "batch_id", batchID, "source", l.GetSource(), "err", err)
			}
		}
	}

	for _, i := range batch.GetInventories() {
		if r.Inventories == nil {
			slog.Error("ingestreceiver: dropping inventory because no inventory store is configured", "host_id", hostID, "batch_id", batchID, "kind", i.GetKind())
			continue
		}
		inventory := protoToInventory(i)
		if err := inventory.Validate(); err != nil {
			r.recordValidationRejection(ctx, hostID, "inventory", i.GetKind(), err)
			continue
		}
		if err := r.Inventories.UpsertInventory(ctx, inventory); err != nil {
			slog.Error("ingestreceiver: dropping inventory", "host_id", hostID, "batch_id", batchID, "kind", i.GetKind(), "err", err)
		}
	}
	for _, j := range batch.GetJobs() {
		if r.Jobs == nil {
			slog.Error("ingestreceiver: dropping job because no job store is configured", "host_id", hostID, "batch_id", batchID, "job_id", j.GetId())
			continue
		}
		job := protoToJob(j)
		if err := job.Validate(); err != nil {
			r.recordValidationRejection(ctx, hostID, "job", j.GetId(), err)
			continue
		}
		if err := r.Jobs.InsertJob(ctx, job); err != nil {
			slog.Error("ingestreceiver: dropping job", "host_id", hostID, "batch_id", batchID, "job_id", j.GetId(), "err", err)
		}
	}

	return nil
}

func (r *Receiver) recordValidationRejection(ctx context.Context, hostID, dataKind, name string, validationErr error) {
	reason := validationErr.Error()
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", hostID, dataKind, name, reason)
	now := r.now()
	if !r.rejections.allow(key, now) {
		return
	}

	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	window := now.Truncate(validationRejectionInterval).Unix()
	event := schema.Event{
		ID:         fmt.Sprintf("validation-rejection-%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", key, window)))),
		TS:         now,
		TSReceived: now,
		HostID:     hostID,
		Source:     "ingestreceiver",
		Type:       "ingest.validation_rejected",
		Severity:   schema.SeverityWarn,
		Title:      fmt.Sprintf("Rejected invalid %s during ingest", dataKind),
		Attrs: schema.Labels{
			"data_kind": dataKind,
			"data_name": name,
			"reason":    reason,
		},
		Fingerprint: fingerprint,
		Schema:      schema.CurrentSchemaVersion,
	}
	if err := r.Events.InsertEvent(ctx, event); err != nil {
		slog.Error("ingestreceiver: recording validation rejection", "host_id", hostID, "data_kind", dataKind, "name", name, "err", err)
	}
}
