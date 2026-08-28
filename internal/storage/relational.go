// Package storage implements Bitácora's relational storage layer — "Capa 3"
// of ADR-0003: events, jobs, alerts and their history, hosts, capability
// manifests, the log block index, config, users and tokens.
//
// Only the Event-related methods are implemented so far; the rest need
// their own canonical schema types (Job, Alert, Host) before a Relational
// method can be written against them meaningfully — see the followups on
// this task.
package storage

import (
	"context"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Relational is the storage interface every backend implements. The
// interface must stay backend-agnostic so a PostgreSQL implementation can
// be added later without touching any caller (ADR-0003).
type Relational interface {
	// InsertEvent stores one event. Inserting the same event ID twice is a
	// no-op, not an error — helpers and collectors may retry.
	InsertEvent(ctx context.Context, e schema.Event) error

	// ListEvents returns every event in [from, to] for hostID, or for every
	// host if hostID is empty, ordered by ts ascending.
	ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error)

	// SearchEventTitles returns events whose title matches an FTS5 query
	// (see https://www.sqlite.org/fts5.html for query syntax), most
	// recent first.
	SearchEventTitles(ctx context.Context, query string, limit int) ([]schema.Event, error)

	// Close releases every resource the store holds open.
	Close() error
}
