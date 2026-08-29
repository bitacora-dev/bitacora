package alerting

import (
	"testing"
	"time"
)

func TestManager_DedupSameFingerprintDoesNotDoubleFire(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()
	labels := map[string]string{"host_id": "host-a"}

	_, notify1 := m.Evaluate(now, "cpu-temp-alta", labels, "warn", true, 90, 0)
	_, notify2 := m.Evaluate(now.Add(time.Second), "cpu-temp-alta", labels, "warn", true, 91, 0)

	if !notify1 {
		t.Fatal("expected the first evaluation to notify (fresh firing)")
	}
	if notify2 {
		t.Fatal("expected the second evaluation for the same rule+labels not to re-notify — same fingerprint, already firing")
	}
}

func TestManager_DifferentLabelsAreIndependentAlerts(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()

	_, notifyA := m.Evaluate(now, "cpu-temp-alta", map[string]string{"host_id": "host-a"}, "warn", true, 90, 0)
	_, notifyB := m.Evaluate(now, "cpu-temp-alta", map[string]string{"host_id": "host-b"}, "warn", true, 90, 0)

	if !notifyA || !notifyB {
		t.Fatal("expected the same rule on two different hosts to be two independent alerts, both notifying")
	}
	if len(m.All()) != 2 {
		t.Fatalf("expected 2 tracked alerts, got %d", len(m.All()))
	}
}

func TestManager_SilencedTransitionDoesNotNotify(t *testing.T) {
	silences := NewSilenceStore()
	now := time.Now()
	sil, err := NewSilence(map[string]string{"host_id": "host-a"}, now.Add(-time.Minute), now.Add(time.Hour), "nacho", "maintenance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	silences.Add(sil)

	m := NewManager(silences)
	alert, notify := m.Evaluate(now, "cpu-temp-alta", map[string]string{"host_id": "host-a"}, "warn", true, 90, 0)

	if notify {
		t.Fatal("expected a silenced alert not to notify")
	}
	// But the state machine itself still moved — silences suppress
	// notification, not state or history.
	if alert.State != StateFiring {
		t.Fatalf("expected the alert to still transition to firing despite being silenced, got %s", alert.State)
	}
}

func TestManager_GetFindsAnEvaluatedAlert(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()
	labels := map[string]string{"host_id": "host-a"}
	m.Evaluate(now, "cpu-temp-alta", labels, "warn", true, 90, 0)

	got, ok := m.Get("cpu-temp-alta", labels)
	if !ok {
		t.Fatal("expected to find the alert")
	}
	if got.State != StateFiring {
		t.Fatalf("expected firing, got %s", got.State)
	}
}

func TestManager_FiringListsOnlyCurrentlyFiring(t *testing.T) {
	m := NewManager(nil)
	now := time.Now()

	m.Evaluate(now, "rule-a", map[string]string{"host_id": "host-a"}, "warn", true, 90, 0)  // firing
	m.Evaluate(now, "rule-b", map[string]string{"host_id": "host-a"}, "warn", false, 10, 0) // stays inactive

	firing := m.Firing()
	if len(firing) != 1 || firing[0].RuleID != "rule-a" {
		t.Fatalf("expected exactly rule-a to be firing, got %+v", firing)
	}
}
