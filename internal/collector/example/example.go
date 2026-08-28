// Package example is the canonical reference Collector implementation.
// Read this before writing any other collector (ADR-0007): it requires no
// host capability and demonstrates the correct shape — Init/Collect/Close,
// context-respecting work, no global state, no time.Sleep.
package example

import (
	"context"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// Collector emits a single heartbeat counter and event on every tick.
type Collector struct {
	ticks int
}

// New returns a ready-to-register example collector.
func New() *Collector {
	return &Collector{}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "example" }

// Requires implements collector.Collector. The example needs nothing.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector. Nothing to prepare.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.ticks++
	sink.Counter("bitacora_example_ticks_total", float64(c.ticks), collector.Labels{"collector": c.Name()})
	sink.Event(collector.Event{
		Type:      "example.tick",
		Level:     "info",
		Message:   "example collector ticked",
		Timestamp: time.Now(),
	})
	return nil
}

// Close implements collector.Collector. Nothing to release.
func (c *Collector) Close() error { return nil }
