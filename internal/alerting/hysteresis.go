package alerting

// HysteresisThreshold implements ADR-0009's hysteresis: separate fire and
// resolve thresholds, so a value oscillating right at the boundary
// doesn't flap between firing and resolved. "Dispara a 85°C, resuelve a
// 75°C."
type HysteresisThreshold struct {
	FireAbove    float64
	ResolveBelow float64
}

// ConditionTrue reports whether the alert condition should be considered
// met, given the current value and whether it was already firing (or
// pending) going into this evaluation. Once triggered, the condition
// stays true until the value drops below ResolveBelow — not merely below
// FireAbove.
func (h HysteresisThreshold) ConditionTrue(value float64, wasActive bool) bool {
	if wasActive {
		return !(value < h.ResolveBelow)
	}
	return value > h.FireAbove
}
