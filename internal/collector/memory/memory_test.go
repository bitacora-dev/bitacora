package memory

import (
	"context"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

type recordingSink struct {
	gauges map[string]float64
}

func newRecordingSink() *recordingSink { return &recordingSink{gauges: map[string]float64{}} }

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	s.gauges[name] = value
}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}
func (s *recordingSink) Inventory(collector.Inventory)             {}

func TestCollector_ReadsFixtureAndEmitsExpectedValues(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"procfs_path": "testdata/procfs"}, nil); err != nil {
		t.Fatalf("unexpected error initializing against the fixture: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error collecting: %v", err)
	}

	// Fixture: MemTotal 16384000 kB, MemAvailable 8192000 kB, SwapTotal
	// 2097148 kB, SwapFree 2097148 kB.
	wantTotal := 16384000.0 * 1024
	if got := sink.gauges["bitacora_memory_total_bytes"]; got != wantTotal {
		t.Fatalf("expected total %v bytes, got %v", wantTotal, got)
	}

	wantAvailable := 8192000.0 * 1024
	if got := sink.gauges["bitacora_memory_available_bytes"]; got != wantAvailable {
		t.Fatalf("expected available %v bytes, got %v", wantAvailable, got)
	}

	wantUsedRatio := (16384000.0 - 8192000.0) / 16384000.0
	if got := sink.gauges["bitacora_memory_used_ratio"]; got != wantUsedRatio {
		t.Fatalf("expected used ratio %v, got %v", wantUsedRatio, got)
	}

	wantSwapTotal := 2097148.0 * 1024
	if got := sink.gauges["bitacora_memory_swap_total_bytes"]; got != wantSwapTotal {
		t.Fatalf("expected swap total %v bytes, got %v", wantSwapTotal, got)
	}

	wantSwapFree := 2097148.0 * 1024
	if got := sink.gauges["bitacora_memory_swap_free_bytes"]; got != wantSwapFree {
		t.Fatalf("expected swap free %v bytes, got %v", wantSwapFree, got)
	}
}

func TestCollector_UsedRatioIsWithinZeroToOne(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"procfs_path": "testdata/procfs"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ratio := sink.gauges["bitacora_memory_used_ratio"]
	if ratio < 0 || ratio > 1 {
		t.Fatalf("used ratio must be within [0,1] per ADR-0006, got %v", ratio)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"procfs_path": "testdata/procfs"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Collect(ctx, newRecordingSink()); err == nil {
		t.Fatal("expected Collect to return an error for an already-cancelled context")
	}
}
