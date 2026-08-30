package alerting

import (
	"sync"
	"time"
)

// Manager owns every Alert, keyed by fingerprint (deduplication) and
// checks silences before deciding whether a fresh transition should
// notify.
type Manager struct {
	silences *SilenceStore

	mu     sync.Mutex
	alerts map[string]*Alert
}

// NewManager returns a Manager. silences may be nil to disable silencing
// (nothing is ever suppressed).
func NewManager(silences *SilenceStore) *Manager {
	return &Manager{silences: silences, alerts: make(map[string]*Alert)}
}

// Evaluate looks up (creating if needed) the alert identified by
// (ruleID, labels) and advances its state machine. It returns the alert
// and whether this evaluation should trigger a notification: a fresh
// firing or resolved transition that isn't currently silenced.
//
// A silenced transition still happens — state, history and dedup are all
// unaffected by silences, which only suppress the *notification*
// (ADR-0009 doesn't say silences should hide alerts from history, only
// from paging someone).
func (m *Manager) Evaluate(now time.Time, ruleID string, labels map[string]string, severity string, conditionTrue bool, value float64, forDuration time.Duration) (alert *Alert, shouldNotify bool) {
	fp := Fingerprint(ruleID, labels)

	m.mu.Lock()
	a, ok := m.alerts[fp]
	if !ok {
		a = NewAlert(fp, ruleID, labels, severity)
		m.alerts[fp] = a
	}
	m.mu.Unlock()

	transitioned := a.EvaluateFor(now, conditionTrue, value, forDuration)
	if !transitioned {
		return a, false
	}

	if m.silences != nil && m.silences.Silenced(now, labels) {
		return a, false
	}
	return a, true
}

// Get returns the alert for (ruleID, labels) if one has ever been
// evaluated, for status/UI lookups.
func (m *Manager) Get(ruleID string, labels map[string]string) (*Alert, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.alerts[Fingerprint(ruleID, labels)]
	return a, ok
}

// Firing returns every alert currently in the firing state.
func (m *Manager) Firing() []*Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Alert
	for _, a := range m.alerts {
		if a.State == StateFiring {
			out = append(out, a)
		}
	}
	return out
}

// All returns every alert the manager has ever evaluated, firing or not —
// for a full status listing.
func (m *Manager) All() []*Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		out = append(out, a)
	}
	return out
}
