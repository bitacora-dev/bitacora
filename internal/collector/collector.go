// Package collector defines the Collector contract, its runtime and its
// registry (ADR-0007). Read internal/collector/example/ before writing a
// new collector.
package collector

import (
	"context"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Capability identifies something the host must support for a collector to
// run (ADR-0004). The concrete set of capabilities and how they're probed
// is defined elsewhere; this package only checks membership.
type Capability string

// Labels are metric and event label pairs — an alias for schema.Labels, so
// a collector's own construction of an Event/Metric needs no conversion at
// the Sink boundary.
type Labels = schema.Labels

// Event is emitted by collectors and by the runtime itself, through Sink.
// Alias for schema.Event (ADR-0006) — collector.go originally defined its
// own minimal placeholder shape here; aliased once a real collector
// (journald) needed the canonical fields (host_id, severity, etc.) that
// the placeholder didn't have. See #646's followup note.
type Event = schema.Event

// LogLine is one line of log output. Alias for schema.LogLine (ADR-0006),
// same reasoning as Event above.
type LogLine = schema.LogLine

// Inventory is a declarative list snapshot. Alias for schema.Inventory
// (ADR-0015) — the fourth canonical data shape, for collectors reporting
// list-shaped data (shares, VMs, users, ...) rather than a time series or
// a discrete occurrence.
type Inventory = schema.Inventory

// Sink is where a Collector writes everything it produces. Collectors never
// touch storage, the network, or the hub directly (ADR-0002) — they only
// know about Sink.
type Sink interface {
	Gauge(name string, value float64, labels Labels)
	Counter(name string, value float64, labels Labels)
	Event(e Event)
	LogLines(source string, lines []LogLine)
	Inventory(inv Inventory)
}

// HostInfo describes the machine the agent runs on.
type HostInfo struct {
	ID       string
	Hostname string
}

// Config is the collector-specific configuration block resolved by the
// runtime before Init.
type Config map[string]any

// Collector is the contract every metric/event/log source implements.
// See ADR-0007.
type Collector interface {
	// Name is a stable identity. Appears in metrics, events and configuration.
	Name() string

	// Requires lists the capabilities (ADR-0004) this collector needs. If one
	// is missing, the runtime never registers it and emits
	// agent.collector_disabled instead.
	Requires() []Capability

	// Init prepares the collector: open files, resolve paths, validate config.
	// An error here disables the collector; it never aborts the agent.
	Init(ctx context.Context, cfg Config, host *HostInfo) error

	// Collect performs one collection. It MUST respect ctx cancellation and
	// MUST NOT block past the context's deadline. No time.Sleep — wait on
	// ctx via select if you need to.
	Collect(ctx context.Context, sink Sink) error

	// Close releases any resources acquired by Init.
	Close() error
}
