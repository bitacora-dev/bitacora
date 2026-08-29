package alerting

import "time"

// DeadmanTracker watches for a periodic signal (a job run, an agent
// report, a helper writing to the spool, a collector producing samples)
// and reports it overdue once too much time has passed without one —
// ADR-0009: "el tipo de regla más importante del sistema... esperaba una
// ejecución cada 24h y llevo 31h sin verla."
type DeadmanTracker struct {
	ExpectEvery time.Duration
	Grace       time.Duration

	lastSeen time.Time
}

// NewDeadmanTracker returns a tracker that hasn't observed anything yet.
// A never-observed tracker is deliberately not overdue (see Overdue) —
// seed lastSeen via Observe at the moment monitoring starts if "no signal
// since boot" should itself count as overdue.
func NewDeadmanTracker(expectEvery, grace time.Duration) *DeadmanTracker {
	return &DeadmanTracker{ExpectEvery: expectEvery, Grace: grace}
}

// Observe records that the expected signal was seen at t. Out-of-order
// observations (an old one arriving after a newer one) never move
// lastSeen backwards.
func (d *DeadmanTracker) Observe(t time.Time) {
	if t.After(d.lastSeen) {
		d.lastSeen = t
	}
}

// LastSeen returns the last observed time, or the zero Time if nothing
// has been observed yet.
func (d *DeadmanTracker) LastSeen() time.Time { return d.lastSeen }

// Overdue reports whether, as of now, more than ExpectEvery+Grace has
// elapsed since the last observation, and how long it's been. A tracker
// that has never observed anything is not overdue — that's a bootstrap
// state, not an absence, until the caller decides otherwise (e.g. by
// calling Observe once at startup).
func (d *DeadmanTracker) Overdue(now time.Time) (overdue bool, since time.Duration) {
	if d.lastSeen.IsZero() {
		return false, 0
	}
	elapsed := now.Sub(d.lastSeen)
	if elapsed > d.ExpectEvery+d.Grace {
		return true, elapsed
	}
	return false, 0
}
