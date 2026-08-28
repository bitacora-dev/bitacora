package cpu

import (
	"context"
	"testing"

	"github.com/prometheus/procfs"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

type recordingSink struct {
	gauges []gaugeCall
}

type gaugeCall struct {
	name   string
	value  float64
	labels collector.Labels
}

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	s.gauges = append(s.gauges, gaugeCall{name, value, labels})
}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}

func TestCollector_FirstCollectEmitsNothingButDoesNotError(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"procfs_path": "testdata/procfs"}, nil); err != nil {
		t.Fatalf("unexpected error initializing against the fixture: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error on first collect: %v", err)
	}
	if len(sink.gauges) != 0 {
		t.Fatalf("expected no gauges on the first collect (no baseline to diff against), got %+v", sink.gauges)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"procfs_path": "testdata/procfs"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected Collect to return an error for an already-cancelled context")
	}
}

func TestCpuUsageRatio(t *testing.T) {
	prev := procfs.CPUStat{User: 100, Nice: 0, System: 50, Idle: 850, Iowait: 0}
	cur := procfs.CPUStat{User: 150, Nice: 0, System: 70, Idle: 870, Iowait: 0}
	// totalDelta = (150+70+870) - (100+50+850) = 90; idleDelta = 870-850 = 20;
	// busyDelta = 90-20 = 70 (matches the +50 user, +20 system deltas).

	ratio, ok := cpuUsageRatio(prev, cur)
	if !ok {
		t.Fatal("expected ok=true when counters advance")
	}
	want := 70.0 / 90.0
	if diff := ratio - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected ratio %.6f, got %.6f", want, ratio)
	}
}

func TestCpuUsageRatio_NoAdvanceReturnsNotOK(t *testing.T) {
	same := procfs.CPUStat{User: 100, Idle: 900}
	if _, ok := cpuUsageRatio(same, same); ok {
		t.Fatal("expected ok=false when the counters didn't move")
	}
}

func TestCpuUsageRatio_AllIdleIsZero(t *testing.T) {
	prev := procfs.CPUStat{Idle: 1000}
	cur := procfs.CPUStat{Idle: 1100}
	ratio, ok := cpuUsageRatio(prev, cur)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ratio != 0 {
		t.Fatalf("expected 0 usage when only idle advances, got %v", ratio)
	}
}

func TestCpuUsageRatio_AllBusyIsOne(t *testing.T) {
	prev := procfs.CPUStat{User: 1000, Idle: 0}
	cur := procfs.CPUStat{User: 1100, Idle: 0}
	ratio, ok := cpuUsageRatio(prev, cur)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ratio != 1 {
		t.Fatalf("expected 1.0 usage when nothing but user time advances, got %v", ratio)
	}
}
