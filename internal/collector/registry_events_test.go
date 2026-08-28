package collector

import (
	"context"
	"testing"
	"time"
)

type recordingSink struct {
	events []Event
}

func (s *recordingSink) Gauge(string, float64, Labels)   {}
func (s *recordingSink) Counter(string, float64, Labels) {}
func (s *recordingSink) Event(e Event)                   { s.events = append(s.events, e) }
func (s *recordingSink) LogLines(string, []LogLine)      {}

func TestEmitDisabledEvents_EmitsAgentCollectorDisabled(t *testing.T) {
	sink := &recordingSink{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	EmitDisabledEvents(sink, []Disabled{{Name: "smart", Reason: `missing capability "storage.smart"`}}, now)

	if len(sink.events) != 1 {
		t.Fatalf("expected one event, got %d", len(sink.events))
	}
	got := sink.events[0]
	if got.Type != "agent.collector_disabled" {
		t.Errorf("expected type agent.collector_disabled, got %q", got.Type)
	}
	if got.Attrs["collector"] != "smart" {
		t.Errorf("expected collector attr to name the disabled collector, got %+v", got.Attrs)
	}
	if !got.Timestamp.Equal(now) {
		t.Errorf("expected timestamp %v, got %v", now, got.Timestamp)
	}
}

// TestResolveThenEmit_FakeCapabilityDisablesOnlyTheRightCollector is the
// end-to-end shape of the acceptance criterion: given an available-set that
// is missing exactly one made-up capability, only the collector that
// declared it goes to Disabled (and the others still register), and that
// disablement is what gets turned into an event.
func TestResolveThenEmit_FakeCapabilityDisablesOnlyTheRightCollector(t *testing.T) {
	reg := &Registry{}
	reg.Register(&stubCollector{name: "needs-fake", requires: []Capability{"totally.made-up"}}, time.Second, time.Second)
	reg.Register(&stubCollector{name: "needs-nothing"}, time.Second, time.Second)

	available := map[Capability]bool{"totally.made-up": false, "something.else": true}
	regs, disabled := reg.Resolve(context.Background(), Config{}, &HostInfo{}, available)

	if len(regs) != 1 || regs[0].Collector.Name() != "needs-nothing" {
		t.Fatalf("expected only needs-nothing to register, got %+v", regs)
	}
	if len(disabled) != 1 || disabled[0].Name != "needs-fake" {
		t.Fatalf("expected only needs-fake to be disabled, got %+v", disabled)
	}

	sink := &recordingSink{}
	EmitDisabledEvents(sink, disabled, time.Now())
	if len(sink.events) != 1 || sink.events[0].Attrs["collector"] != "needs-fake" {
		t.Fatalf("expected exactly one disabled event naming needs-fake, got %+v", sink.events)
	}
}
