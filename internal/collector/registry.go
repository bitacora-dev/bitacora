package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/oklog/ulid/v2"
)

// disabledEventType is the event emitted once per disabled collector
// (ADR-0004: "emitiendo un evento informativo una sola vez").
const disabledEventType = "agent.collector_disabled"

// EmitDisabledEvents turns the Disabled entries returned by Resolve into
// agent.collector_disabled events on sink, so a missing capability or a
// failed Init is visible in the timeline and not just in a log line.
func EmitDisabledEvents(sink Sink, hostID string, disabled []Disabled, now time.Time) {
	for _, d := range disabled {
		sink.Event(Event{
			ID:       ulid.Make().String(),
			TS:       now,
			HostID:   hostID,
			Source:   "agent",
			Type:     disabledEventType,
			Severity: schema.SeverityInfo,
			Title:    fmt.Sprintf("collector %q disabled: %s", d.Name, d.Reason),
			Attrs:    Labels{"collector": d.Name, "reason": d.Reason},
			Schema:   schema.CurrentSchemaVersion,
		})
	}
}

type registryItem struct {
	collector Collector
	interval  time.Duration
	timeout   time.Duration
}

// Registry holds collectors and resolves which ones can run given the
// capabilities available on this host (ADR-0004).
type Registry struct {
	items []registryItem
}

// Register adds a collector with its scheduling parameters. It does not
// call Init — that happens in Resolve, once capabilities are known.
func (reg *Registry) Register(c Collector, interval, timeout time.Duration) {
	reg.items = append(reg.items, registryItem{collector: c, interval: interval, timeout: timeout})
}

// Disabled describes a collector that was not registered at runtime,
// and why.
type Disabled struct {
	Name   string
	Reason string
}

// Resolve checks each registered collector's Requires() against the
// available capabilities, calls Init on the ones that qualify, and returns
// the resulting Registrations plus the collectors that were disabled.
func (reg *Registry) Resolve(ctx context.Context, cfg Config, host *HostInfo, available map[Capability]bool) ([]Registration, []Disabled) {
	var regs []Registration
	var disabled []Disabled

	for _, item := range reg.items {
		if missing, ok := firstMissing(item.collector.Requires(), available); ok {
			disabled = append(disabled, Disabled{
				Name:   item.collector.Name(),
				Reason: fmt.Sprintf("missing capability %q", missing),
			})
			continue
		}

		if err := item.collector.Init(ctx, cfg, host); err != nil {
			disabled = append(disabled, Disabled{
				Name:   item.collector.Name(),
				Reason: fmt.Sprintf("init failed: %v", err),
			})
			continue
		}

		regs = append(regs, Registration{
			Collector: item.collector,
			Interval:  item.interval,
			Timeout:   item.timeout,
		})
	}

	return regs, disabled
}

func firstMissing(required []Capability, available map[Capability]bool) (Capability, bool) {
	for _, c := range required {
		if !available[c] {
			return c, true
		}
	}
	return "", false
}
