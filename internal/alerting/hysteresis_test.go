package alerting

import "testing"

func TestHysteresisThreshold_FiresAboveFireThreshold(t *testing.T) {
	h := HysteresisThreshold{FireAbove: 85, ResolveBelow: 75}
	if !h.ConditionTrue(90, false) {
		t.Fatal("expected 90 > 85 to fire when not already active")
	}
}

func TestHysteresisThreshold_DoesNotFireBetweenThresholdsWhenInactive(t *testing.T) {
	h := HysteresisThreshold{FireAbove: 85, ResolveBelow: 75}
	if h.ConditionTrue(80, false) {
		t.Fatal("expected 80 (below FireAbove) not to fire when not already active")
	}
}

func TestHysteresisThreshold_StaysActiveBetweenThresholds(t *testing.T) {
	h := HysteresisThreshold{FireAbove: 85, ResolveBelow: 75}
	// This is the whole point of hysteresis: 80 is below the fire
	// threshold but above the resolve threshold — an already-firing
	// alert must NOT resolve here, or a value oscillating at the
	// boundary would flap.
	if !h.ConditionTrue(80, true) {
		t.Fatal("expected an already-active alert to stay active between the two thresholds")
	}
}

func TestHysteresisThreshold_ResolvesBelowResolveThreshold(t *testing.T) {
	h := HysteresisThreshold{FireAbove: 85, ResolveBelow: 75}
	if h.ConditionTrue(70, true) {
		t.Fatal("expected 70 < 75 to resolve an already-active alert")
	}
}
