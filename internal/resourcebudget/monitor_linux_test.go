//go:build linux

package resourcebudget

import (
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

type eventSink struct{ events []schema.Event }

func (s *eventSink) Event(event schema.Event) { s.events = append(s.events, event) }

func TestMonitor_EmitsOnceAfterCrossingBudget(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	now := base
	samples := []Measurement{
		{RSSBytes: 10 * 1024 * 1024, CPUSeconds: 1},
		{RSSBytes: MaxRSSBytes + 1, CPUSeconds: 1.01},
		{RSSBytes: MaxRSSBytes + 2, CPUSeconds: 1.02},
	}
	index := 0
	sink := &eventSink{}
	monitor := Monitor{
		HostID: "host-a",
		Sink:   sink,
		Now:    func() time.Time { return now },
		Sample: func(int) (uint64, float64, error) {
			sample := samples[index]
			index++
			return sample.RSSBytes, sample.CPUSeconds, nil
		},
	}

	if err := monitor.Observe(1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := monitor.Observe(1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := monitor.Observe(1); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("expected one budget event, got %d", len(sink.events))
	}
	event := sink.events[0]
	if event.Type != "agent.resource_budget_exceeded" || event.HostID != "host-a" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Attrs["rss_bytes"] != "62914561" || event.Attrs["cpu_fraction"] != "0.010000" {
		t.Fatalf("unexpected event attributes: %+v", event.Attrs)
	}
}

func TestMonitor_UsesCPUDeltaInsteadOfCumulativeCPU(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	now := base
	sink := &eventSink{}
	measurements := []Measurement{{RSSBytes: 1, CPUSeconds: 10_000}, {RSSBytes: 1, CPUSeconds: 10_000.01}}
	index := 0
	monitor := Monitor{
		Sink: sink,
		Now:  func() time.Time { return now },
		Sample: func(int) (uint64, float64, error) {
			measurement := measurements[index]
			index++
			return measurement.RSSBytes, measurement.CPUSeconds, nil
		},
	}
	if err := monitor.Observe(1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := monitor.Observe(1); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no event for 1%% delta CPU, got %+v", sink.events)
	}
}
