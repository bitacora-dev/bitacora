package agentbuffer

import (
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// TestSink_BeginCycleStampsEveryEmissionWithTheSameInstant covers the bug
// behind the network panel reading 0 B/s: the plain Sink reads its clock
// once per emission, so a collector emitting two counters per interface
// spread one cycle over as many timestamps as it had emissions, and
// hub-side aggregation by timestamp then never saw a whole cycle at once.
func TestSink_BeginCycleStampsEveryEmissionWithTheSameInstant(t *testing.T) {
	buffer, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	defer buffer.Close()

	// A clock that moves on every read is exactly what the real one does
	// between two consecutive emissions; BeginCycle has to neutralize it.
	tick := time.Unix(1_700_000_000, 0)
	sink := NewSink("host-a", buffer, WithClock(func() time.Time {
		tick = tick.Add(time.Millisecond)
		return tick
	}))

	cycle := sink.BeginCycle(time.Unix(1_700_000_500, 0))
	for _, iface := range []string{"eno1", "eno2", "tailscale0"} {
		cycle.Counter("bitacora_net_rx_bytes_total", 1, schema.Labels{"interface": iface})
		cycle.Counter("bitacora_net_tx_bytes_total", 2, schema.Labels{"interface": iface})
	}

	items, err := buffer.oldestItems(100)
	if err != nil {
		t.Fatalf("unexpected error reading items: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("expected 6 buffered metrics, got %d", len(items))
	}
	want := time.Unix(1_700_000_500, 0)
	for _, item := range items {
		if item.Metric == nil {
			t.Fatalf("expected a metric item, got %+v", item)
		}
		if !item.Metric.Timestamp.Equal(want) {
			t.Fatalf("expected every metric of the cycle at %s, got %s for %s",
				want, item.Metric.Timestamp, item.Metric.Labels["interface"])
		}
	}
}

// TestSink_BeginCycleLeavesTheReceiverUsable asserts BeginCycle does not
// freeze the Sink itself. The runtime runs one goroutine per collector
// against a single shared Sink, so a receiver mutated by one cycle would
// stamp every other collector with that cycle's instant.
func TestSink_BeginCycleLeavesTheReceiverUsable(t *testing.T) {
	buffer, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	defer buffer.Close()

	live := time.Unix(1_700_000_000, 0)
	sink := NewSink("host-a", buffer, WithClock(func() time.Time { return live }))

	frozen := time.Unix(1_600_000_000, 0)
	sink.BeginCycle(frozen).Gauge("bitacora_cpu_usage_ratio", 0.1, schema.Labels{"cpu": "total"})
	sink.Gauge("bitacora_cpu_usage_ratio", 0.2, schema.Labels{"cpu": "total"})

	items, err := buffer.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error reading items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 buffered metrics, got %d", len(items))
	}
	if !items[0].Metric.Timestamp.Equal(frozen) {
		t.Fatalf("expected the cycle sink to use %s, got %s", frozen, items[0].Metric.Timestamp)
	}
	if !items[1].Metric.Timestamp.Equal(live) {
		t.Fatalf("expected the receiver to keep its own clock (%s), got %s", live, items[1].Metric.Timestamp)
	}
}

// TestSink_BeginCyclePreservesExplicitTimestamps asserts the frozen clock
// only fills in what was missing. journald carries the journal's own
// instant on each line and an Inventory its ReportedAt; neither may be
// rewritten to the cycle's instant.
func TestSink_BeginCyclePreservesExplicitTimestamps(t *testing.T) {
	buffer, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	defer buffer.Close()

	sink := NewSink("host-a", buffer, WithClock(func() time.Time { return time.Unix(1_700_000_000, 0) }))
	cycle := sink.BeginCycle(time.Unix(1_700_000_500, 0))

	lineTS := time.Unix(1_699_999_000, 0)
	cycle.LogLines("journald", []schema.LogLine{{Message: "hello", TS: lineTS}})
	reportedAt := time.Unix(1_699_998_000, 0)
	cycle.Inventory(schema.Inventory{Kind: schema.InventoryUser, ReportedAt: reportedAt})

	items, err := buffer.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error reading items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 buffered items, got %d", len(items))
	}
	if !items[0].LogLine.TS.Equal(lineTS) {
		t.Fatalf("expected the log line to keep %s, got %s", lineTS, items[0].LogLine.TS)
	}
	if !items[1].Inventory.ReportedAt.Equal(reportedAt) {
		t.Fatalf("expected the inventory to keep %s, got %s", reportedAt, items[1].Inventory.ReportedAt)
	}
}
