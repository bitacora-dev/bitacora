// Package alerting implements the hub's alert engine (ADR-0009): a state
// machine per alert (inactive → pending → firing → resolved) with `for`,
// hysteresis, deduplication, silences and deadman (absence) rules.
//
// This package deliberately doesn't know how to evaluate a rule's
// condition against metrics or events — that's rule-type-specific logic
// (threshold expressions, event-match counting) that belongs to whatever
// wires this to metricstore/storage. What's here is the part ADR-0009
// itself calls out as the hard, easy-to-get-wrong part: the state machine
// and its guarantees, independent of what's driving it.
package alerting

import "time"

// State is one point in an alert's lifecycle (ADR-0009).
type State string

const (
	StateInactive State = "inactive"
	StatePending  State = "pending"
	StateFiring   State = "firing"
	StateResolved State = "resolved"
)

// Transition records one state change. ADR-0009: "cada transición de
// estado se persiste" — a caller collects these into whatever storage
// backs alert history; this package only guarantees they're recorded
// in-memory on the Alert itself.
type Transition struct {
	From State
	To   State
	At   time.Time
}

// Alert tracks one alert instance's lifecycle. Identified by Fingerprint —
// ADR-0009: "deduplicación por fingerprint de la regla más las
// etiquetas" — so the same underlying condition (same rule, same labels)
// always maps to the same Alert, however many times it's evaluated.
type Alert struct {
	Fingerprint string
	RuleID      string
	Labels      map[string]string
	Severity    string

	State        State
	PendingSince time.Time
	FiringSince  time.Time
	ResolvedAt   time.Time
	LastEval     time.Time
	Value        float64

	History []Transition
}

// NewAlert returns a fresh, inactive alert for the given identity.
func NewAlert(fingerprint, ruleID string, labels map[string]string, severity string) *Alert {
	return &Alert{
		Fingerprint: fingerprint,
		RuleID:      ruleID,
		Labels:      labels,
		Severity:    severity,
		State:       StateInactive,
	}
}

// Evaluate advances the state machine given whether the rule's condition
// is true right now, requiring it to hold continuously for at least
// forDuration before pending becomes firing (ADR-0009: "elimina el ruido
// de los picos transitorios"). conditionTrue should already reflect
// hysteresis if the rule uses it — see HysteresisThreshold.
//
// Returns true exactly when this call caused a *fresh* transition into
// firing or into resolved — the signal a caller uses to decide whether to
// notify. Repeated Evaluate calls while already firing (or already
// inactive) return false: that's the deduplication ADR-0009 asks for,
// falling directly out of the state machine rather than a separate check.
func (a *Alert) Evaluate(now time.Time, conditionTrue bool, value float64) bool {
	return a.EvaluateFor(now, conditionTrue, value, 0)
}

// EvaluateFor is Evaluate with an explicit `for` duration. Evaluate calls
// this with forDuration=0 (fires immediately once true — no minimum
// duration).
func (a *Alert) EvaluateFor(now time.Time, conditionTrue bool, value float64, forDuration time.Duration) bool {
	a.LastEval = now
	a.Value = value

	switch a.State {
	case StateInactive, StateResolved:
		if !conditionTrue {
			return false
		}
		a.transition(StatePending, now)
		a.PendingSince = now
		if forDuration <= 0 {
			a.transition(StateFiring, now)
			a.FiringSince = now
			return true
		}
		return false

	case StatePending:
		if !conditionTrue {
			a.transition(StateInactive, now)
			return false
		}
		if now.Sub(a.PendingSince) >= forDuration {
			a.transition(StateFiring, now)
			a.FiringSince = now
			return true
		}
		return false

	case StateFiring:
		if conditionTrue {
			return false
		}
		a.transition(StateResolved, now)
		a.ResolvedAt = now
		return true

	default:
		return false
	}
}

func (a *Alert) transition(to State, now time.Time) {
	a.History = append(a.History, Transition{From: a.State, To: to, At: now})
	a.State = to
}
