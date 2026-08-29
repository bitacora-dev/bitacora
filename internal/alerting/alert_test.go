package alerting

import (
	"testing"
	"time"
)

func TestAlert_ImmediateFireWithoutFor(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	transitioned := a.Evaluate(now, true, 90)

	if !transitioned {
		t.Fatal("expected a fresh firing transition")
	}
	if a.State != StateFiring {
		t.Fatalf("expected state firing, got %s", a.State)
	}
	if !a.FiringSince.Equal(now) {
		t.Fatalf("expected FiringSince %v, got %v", now, a.FiringSince)
	}
}

func TestAlert_ForDurationDelaysFiring(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	start := time.Now()
	forDuration := 5 * time.Minute

	// First sample: condition becomes true, but `for` hasn't elapsed.
	if a.EvaluateFor(start, true, 90, forDuration) {
		t.Fatal("expected no transition on the first sample — `for` hasn't elapsed")
	}
	if a.State != StatePending {
		t.Fatalf("expected state pending, got %s", a.State)
	}

	// Still within the window.
	if a.EvaluateFor(start.Add(2*time.Minute), true, 90, forDuration) {
		t.Fatal("expected no transition before `for` elapses")
	}
	if a.State != StatePending {
		t.Fatalf("expected still pending, got %s", a.State)
	}

	// `for` has now elapsed.
	firingAt := start.Add(5 * time.Minute)
	if !a.EvaluateFor(firingAt, true, 90, forDuration) {
		t.Fatal("expected a firing transition once `for` elapses")
	}
	if a.State != StateFiring {
		t.Fatalf("expected state firing, got %s", a.State)
	}
	if !a.FiringSince.Equal(firingAt) {
		t.Fatalf("expected FiringSince %v, got %v", firingAt, a.FiringSince)
	}
}

func TestAlert_TransientSpikeBelowForDurationCancels(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	start := time.Now()
	forDuration := 5 * time.Minute

	if a.EvaluateFor(start, true, 90, forDuration) {
		t.Fatal("expected no transition yet")
	}
	if a.State != StatePending {
		t.Fatalf("expected pending, got %s", a.State)
	}

	// Condition disappears before `for` elapses — this is the "ruido de
	// picos transitorios" ADR-0009 says `for` exists to eliminate.
	if a.EvaluateFor(start.Add(1*time.Minute), false, 20, forDuration) {
		t.Fatal("expected no notify-worthy transition when a transient spike cancels")
	}
	if a.State != StateInactive {
		t.Fatalf("expected the alert to cancel back to inactive, got %s", a.State)
	}
}

func TestAlert_ResolvesWhenConditionClears(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	a.Evaluate(now, true, 90) // -> firing

	resolvedAt := now.Add(10 * time.Minute)
	transitioned := a.Evaluate(resolvedAt, false, 20)

	if !transitioned {
		t.Fatal("expected a fresh resolved transition")
	}
	if a.State != StateResolved {
		t.Fatalf("expected state resolved, got %s", a.State)
	}
	if !a.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("expected ResolvedAt %v, got %v", resolvedAt, a.ResolvedAt)
	}
}

func TestAlert_RepeatedFiringEvaluationsDoNotReTransition(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	a.Evaluate(now, true, 90) // -> firing

	for i := 1; i <= 5; i++ {
		if a.Evaluate(now.Add(time.Duration(i)*time.Minute), true, 91) {
			t.Fatalf("expected no re-transition on repeated firing evaluation %d — this is the dedup ADR-0009 asks for", i)
		}
		if a.State != StateFiring {
			t.Fatalf("expected to stay firing, got %s", a.State)
		}
	}
}

func TestAlert_InactiveStaysInactiveWhileConditionFalse(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	if a.Evaluate(now, false, 10) {
		t.Fatal("expected no transition while the condition is false")
	}
	if a.State != StateInactive {
		t.Fatalf("expected state inactive, got %s", a.State)
	}
}

func TestAlert_ResolvedCanFireAgain(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	a.Evaluate(now, true, 90)                   // -> firing
	a.Evaluate(now.Add(time.Minute), false, 20) // -> resolved

	refiredAt := now.Add(2 * time.Minute)
	if !a.Evaluate(refiredAt, true, 95) {
		t.Fatal("expected a resolved alert to be able to fire again")
	}
	if a.State != StateFiring {
		t.Fatalf("expected state firing again, got %s", a.State)
	}
}

func TestAlert_HistoryRecordsEveryTransition(t *testing.T) {
	a := NewAlert("fp", "rule-1", nil, "warn")
	now := time.Now()

	a.Evaluate(now, true, 90)                   // inactive -> pending -> firing (immediate, for=0)
	a.Evaluate(now.Add(time.Minute), false, 20) // firing -> resolved

	if len(a.History) != 3 {
		t.Fatalf("expected 3 recorded transitions (inactive->pending, pending->firing, firing->resolved), got %d: %+v", len(a.History), a.History)
	}
	if a.History[0].From != StateInactive || a.History[0].To != StatePending {
		t.Fatalf("unexpected first transition: %+v", a.History[0])
	}
	if a.History[len(a.History)-1].To != StateResolved {
		t.Fatalf("expected the last transition to land on resolved, got %+v", a.History[len(a.History)-1])
	}
}
