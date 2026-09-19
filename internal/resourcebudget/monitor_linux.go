//go:build linux

package resourcebudget

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/oklog/ulid/v2"
)

// EventSink is the narrow boundary the budget monitor needs to make a budget
// breach visible in the agent timeline.
type EventSink interface {
	Event(schema.Event)
}

// Measurement is one point-in-time process resource sample.
type Measurement struct {
	RSSBytes    uint64
	CPUSeconds  float64
	CollectedAt time.Time
}

// Monitor compares consecutive process samples. RSS is instantaneous; CPU is
// a fraction over the interval between samples, never the process's cumulative
// CPU time divided by an assumed duration.
type Monitor struct {
	HostID string
	Sink   EventSink
	Now    func() time.Time
	Sample func(pid int) (rssBytes uint64, cpuSeconds float64, err error)

	previous *Measurement
	alerted  bool
}

// Observe records one sample and emits exactly one event when the process
// first crosses an ADR-0001 limit. A first sample establishes the CPU baseline.
func (m *Monitor) Observe(pid int) error {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	sample := Sample
	if m.Sample != nil {
		sample = m.Sample
	}
	rssBytes, cpuSeconds, err := sample(pid)
	if err != nil {
		return err
	}
	current := Measurement{RSSBytes: rssBytes, CPUSeconds: cpuSeconds, CollectedAt: now}
	if m.previous == nil {
		m.previous = &current
		return nil
	}

	window := current.CollectedAt.Sub(m.previous.CollectedAt).Seconds()
	if window <= 0 {
		return fmt.Errorf("resource budget sample window must be positive")
	}
	cpuFraction := (current.CPUSeconds - m.previous.CPUSeconds) / window
	if cpuFraction < 0 {
		return fmt.Errorf("resource budget CPU time moved backwards")
	}
	m.previous = &current
	if err := CheckBudget(current.RSSBytes, cpuFraction); err != nil && !m.alerted {
		m.alerted = true
		m.emit(current, cpuFraction, window, err)
	}
	return nil
}

func (m *Monitor) emit(sample Measurement, cpuFraction, window float64, breach error) {
	if m.Sink == nil {
		return
	}
	m.Sink.Event(schema.Event{
		ID:       ulid.Make().String(),
		TS:       sample.CollectedAt,
		HostID:   m.HostID,
		Source:   "agent",
		Type:     "agent.resource_budget_exceeded",
		Severity: schema.SeverityWarn,
		Title:    "agent resource budget exceeded",
		Attrs: schema.Labels{
			"reason":         breach.Error(),
			"rss_bytes":      strconv.FormatUint(sample.RSSBytes, 10),
			"cpu_fraction":   strconv.FormatFloat(cpuFraction, 'f', 6, 64),
			"window_seconds": strconv.FormatFloat(window, 'f', 6, 64),
		},
		Schema: schema.CurrentSchemaVersion,
	})
}

// Run samples immediately to establish a baseline and then at interval until
// ctx ends. Sampling errors are returned to the caller so they are never
// silently mistaken for a compliant process.
func (m *Monitor) Run(ctx context.Context, pid int, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("resource budget interval must be positive")
	}
	if err := m.Observe(pid); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.Observe(pid); err != nil {
				return err
			}
		}
	}
}
