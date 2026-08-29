package alerting

import (
	"testing"
	"time"
)

func TestDeadmanTracker_NeverObservedIsNotOverdue(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	overdue, _ := d.Overdue(time.Now())
	if overdue {
		t.Fatal("expected a tracker that has never observed anything not to be overdue — that's a bootstrap state, not an absence")
	}
}

func TestDeadmanTracker_NotOverdueWithinExpectedWindow(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	start := time.Now()
	d.Observe(start)

	overdue, _ := d.Overdue(start.Add(20 * time.Hour))
	if overdue {
		t.Fatal("expected no overdue within the expected 24h window")
	}
}

func TestDeadmanTracker_NotOverdueWithinGrace(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	start := time.Now()
	d.Observe(start)

	// ADR-0009's own example: "esperaba una ejecución cada 24h y llevo
	// 31h sin verla" is overdue; 24h+grace exactly at the edge should
	// not be.
	overdue, _ := d.Overdue(start.Add(29 * time.Hour))
	if overdue {
		t.Fatal("expected 29h (within 24h+6h grace) not to be overdue yet")
	}
}

func TestDeadmanTracker_OverdueAfterExpectPlusGrace(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	start := time.Now()
	d.Observe(start)

	overdue, since := d.Overdue(start.Add(31 * time.Hour))
	if !overdue {
		t.Fatal("expected 31h (past 24h+6h grace) to be overdue — ADR-0009's own example")
	}
	if since != 31*time.Hour {
		t.Fatalf("expected since=31h, got %v", since)
	}
}

func TestDeadmanTracker_ObserveResetsOverdue(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	start := time.Now()
	d.Observe(start)

	overdue, _ := d.Overdue(start.Add(31 * time.Hour))
	if !overdue {
		t.Fatal("expected overdue before the fresh observation")
	}

	freshRun := start.Add(31 * time.Hour)
	d.Observe(freshRun)

	overdue, _ = d.Overdue(freshRun.Add(time.Hour))
	if overdue {
		t.Fatal("expected a fresh observation to clear the overdue state")
	}
}

func TestDeadmanTracker_OutOfOrderObservationsNeverMoveBackwards(t *testing.T) {
	d := NewDeadmanTracker(24*time.Hour, 6*time.Hour)
	now := time.Now()
	d.Observe(now)
	d.Observe(now.Add(-time.Hour)) // an older, out-of-order observation

	if !d.LastSeen().Equal(now) {
		t.Fatalf("expected LastSeen to stay at the most recent observation, got %v", d.LastSeen())
	}
}
