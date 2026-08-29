package agentbuffer

import (
	"testing"
	"time"
)

// TestEnforceCapacity_DiscardsLogLinesBeforeMetrics checks the priority
// ordering itself (findDiscardVictimLocked), not the full EnforceCapacity
// loop: a byte/age budget small enough to force exactly one discard and no
// more is awkward to construct reliably (it depends on exact JSON
// encoding size), and isn't what this test is actually about.
func TestEnforceCapacity_DiscardsLogLinesBeforeMetrics(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	if _, err := b.Append(metricItem("host-a", 0.1, ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b.mu.Lock()
	victim, ok := b.findDiscardVictimLocked()
	b.mu.Unlock()

	if !ok || kindOf(victim) != "log_line" {
		t.Fatalf("expected the log line to be the discard victim, got ok=%v victim=%+v", ok, victim)
	}
}

// TestEnforceCapacity_DrivesLoopUntilWithinBudget exercises the actual
// EnforceCapacity loop end to end: an unsatisfiable 1-byte budget forces
// it to discard everything discardable (both the log line and the
// metric), stopping only once nothing but the event remains — proving the
// loop doesn't stop after a single discard when it's still over budget.
func TestEnforceCapacity_DrivesLoopUntilWithinBudget(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir, WithCapacity(DefaultMaxAge, 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	if _, err := b.Append(metricItem("host-a", 0.1, ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := b.Append(eventItem("host-a", "segfault", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	discarded, err := b.EnforceCapacity()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(discarded) != 2 {
		t.Fatalf("expected both the log line and the metric discarded (never the event) with an unsatisfiable budget, got %+v", discarded)
	}

	items, err := b.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Event == nil {
		t.Fatalf("expected only the event to remain, got %+v", items)
	}
}

func TestEnforceCapacity_NeverDiscardsEvents(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir, WithCapacity(DefaultMaxAge, 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	if _, err := b.Append(eventItem("host-a", "segfault", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	discarded, err := b.EnforceCapacity()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(discarded) != 0 {
		t.Fatalf("expected nothing discarded when only an event remains, got %+v", discarded)
	}
	if got := b.Len(); got != 1 {
		t.Fatalf("expected the event to still be buffered, got Len()=%d", got)
	}
}

func TestEnforceCapacity_DiscardsOldestWithinPriorityTier(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	seqOld, err := b.Append(logLineItem("host-a", "old", older))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := b.Append(logLineItem("host-a", "new", newer)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b.mu.Lock()
	victim, ok := b.findDiscardVictimLocked()
	b.mu.Unlock()

	if !ok || victim.Seq != seqOld {
		t.Fatalf("expected the older log line (seq %d) to be the discard victim, got ok=%v victim=%+v", seqOld, ok, victim)
	}
}

func TestEnforceCapacity_NoOpWithinBudget(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir) // default generous capacity
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	if _, err := b.Append(logLineItem("host-a", "line", time.Now())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	discarded, err := b.EnforceCapacity()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(discarded) != 0 {
		t.Fatalf("expected nothing discarded while within budget, got %+v", discarded)
	}
}

func TestEnforceCapacity_DiscardsByAge(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir, WithCapacity(time.Minute, DefaultMaxBytes))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	old := time.Now().Add(-time.Hour) // older than the 1-minute max age
	if _, err := b.Append(logLineItem("host-a", "stale", old)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	discarded, err := b.EnforceCapacity()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(discarded) != 1 {
		t.Fatalf("expected the stale item to be discarded on age alone, got %+v", discarded)
	}
}

func TestBuildOverflowEvent_CountsByKind(t *testing.T) {
	discarded := []DiscardedItem{
		{Seq: 1, Kind: "log_line"},
		{Seq: 2, Kind: "log_line"},
		{Seq: 3, Kind: "metric"},
	}
	e := BuildOverflowEvent("host-a", discarded)

	if e.Type != "agent.buffer_overflow" {
		t.Fatalf("expected type agent.buffer_overflow, got %q", e.Type)
	}
	if e.Attrs["log_lines_discarded"] != "2" {
		t.Fatalf("expected 2 log lines counted, got %q", e.Attrs["log_lines_discarded"])
	}
	if e.Attrs["metrics_discarded"] != "1" {
		t.Fatalf("expected 1 metric counted, got %q", e.Attrs["metrics_discarded"])
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("expected the overflow event itself to be a valid schema.Event, got %v", err)
	}
}
