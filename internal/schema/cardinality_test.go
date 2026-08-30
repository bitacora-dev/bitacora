package schema

import (
	"fmt"
	"testing"
	"time"
)

func metricWithLabel(i int) Metric {
	return Metric{
		Name:      "bitacora_test_metric_total",
		HostID:    "host-a",
		Labels:    Labels{"series": fmt.Sprintf("%d", i)},
		Value:     1,
		Timestamp: time.Now(),
	}
}

func TestCardinalityTracker_AllowsUpToTheLimit(t *testing.T) {
	tracker := NewCardinalityTracker(2000)

	for i := 0; i < 2000; i++ {
		if err := tracker.Observe(metricWithLabel(i)); err != nil {
			t.Fatalf("series %d: expected to be accepted within the budget, got %v", i, err)
		}
	}

	if got := tracker.Count("host-a"); got != 2000 {
		t.Fatalf("expected 2000 tracked series, got %d", got)
	}
}

func TestCardinalityTracker_RejectsOverTheLimit(t *testing.T) {
	tracker := NewCardinalityTracker(2000)

	for i := 0; i < 2000; i++ {
		if err := tracker.Observe(metricWithLabel(i)); err != nil {
			t.Fatalf("series %d: unexpected error filling the budget: %v", i, err)
		}
	}

	if err := tracker.Observe(metricWithLabel(2000)); err == nil {
		t.Fatal("expected the 2001st distinct series for the same host to be rejected")
	}

	if got := tracker.Count("host-a"); got != 2000 {
		t.Fatalf("rejected series must not be counted, got %d", got)
	}
}

func TestCardinalityTracker_ReobservingKnownSeriesNeverFails(t *testing.T) {
	tracker := NewCardinalityTracker(1)

	m := metricWithLabel(0)
	if err := tracker.Observe(m); err != nil {
		t.Fatalf("first observation should succeed, got %v", err)
	}
	if err := tracker.Observe(m); err != nil {
		t.Fatalf("re-observing the same series must not fail even at the limit, got %v", err)
	}
}

func TestCardinalityTracker_TracksHostsIndependently(t *testing.T) {
	tracker := NewCardinalityTracker(1)

	a := metricWithLabel(0)
	a.HostID = "host-a"
	b := metricWithLabel(0)
	b.HostID = "host-b"

	if err := tracker.Observe(a); err != nil {
		t.Fatalf("host-a first series should succeed, got %v", err)
	}
	if err := tracker.Observe(b); err != nil {
		t.Fatalf("host-b should have its own independent budget, got %v", err)
	}
}
