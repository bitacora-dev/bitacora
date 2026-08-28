package collector

import (
	"context"
	"fmt"
	"time"
)

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
