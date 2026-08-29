// Package example is the canonical reference Collector implementation.
// Read this before writing any other collector (ADR-0007): it requires no
// host capability and demonstrates the correct shape — Init/Collect/Close,
// context-respecting work, no global state, no time.Sleep.
package example

import (
	"context"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Collector emits a single heartbeat counter and event on every tick.
type Collector struct {
	hostID string
	ticks  int
}

// New returns a ready-to-register example collector.
func New() *Collector {
	return &Collector{}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "example" }

// Requires implements collector.Collector. The example needs nothing.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector. Nothing to prepare beyond
// remembering the host_id every canonical Event needs (ADR-0006).
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	if host != nil {
		c.hostID = host.ID
	}
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
		ID:       fmt.Sprintf("example-%d", c.ticks),
		TS:       time.Now().UTC(),
		HostID:   c.hostID,
		Source:   "example",
		Type:     "example.tick",
		Severity: schema.SeverityInfo,
		Title:    "example collector ticked",
		Schema:   schema.CurrentSchemaVersion,
	})
	return nil
}

// Close implements collector.Collector. Nothing to release.
func (c *Collector) Close() error { return nil }
