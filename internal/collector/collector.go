// Package collector defines the Collector contract, its runtime and its
// registry (ADR-0007). Read internal/collector/example/ before writing a
// new collector.
package collector

import (
	"context"
	"time"
)

// Capability identifies something the host must support for a collector to
// run (ADR-0004). The concrete set of capabilities and how they're probed
// is defined elsewhere; this package only checks membership.
type Capability string

// Labels are metric and event label pairs.
type Labels map[string]string

// Event is emitted by collectors and by the runtime itself, through Sink.
//
// This is a minimal placeholder shape scoped to this package. It will be
// aligned with the canonical event schema (ADR-0006) once that lands.
type Event struct {
	Type      string
	Level     string
	Message   string
	Attrs     Labels
	Timestamp time.Time
}

// LogLine is one line of log output attributed to a source.
type LogLine struct {
	Timestamp time.Time
	Line      string
}

// Sink is where a Collector writes everything it produces. Collectors never
// touch storage, the network, or the hub directly (ADR-0002) — they only
// know about Sink.
type Sink interface {
	Gauge(name string, value float64, labels Labels)
	Counter(name string, value float64, labels Labels)
	Event(e Event)
	LogLines(source string, lines []LogLine)
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
