package alerting

import (
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Silence suppresses notifications for alerts matching Matchers, for a
// bounded window — ADR-0009: "silencios y ventanas de mantenimiento...
// con caducidad obligatoria. Un silencio sin fecha de fin no se permite."
type Silence struct {
	ID        string
	Matchers  map[string]string // label key -> exact value; every matcher must be present in an alert's labels
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
}

// NewSilence validates and constructs a Silence. EndsAt is mandatory and
// must be after StartsAt — the one rule ADR-0009 insists on for this
// feature.
func NewSilence(matchers map[string]string, startsAt, endsAt time.Time, createdBy, comment string) (Silence, error) {
	if endsAt.IsZero() {
		return Silence{}, fmt.Errorf("silence must have an end time (ADR-0009: no open-ended silences)")
	}
	if !endsAt.After(startsAt) {
		return Silence{}, fmt.Errorf("silence end time must be after its start time")
	}
	return Silence{
		ID:        ulid.Make().String(),
		Matchers:  matchers,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		CreatedBy: createdBy,
		Comment:   comment,
	}, nil
}

// Active reports whether the silence is in effect at t.
func (s Silence) Active(t time.Time) bool {
	return !t.Before(s.StartsAt) && t.Before(s.EndsAt)
}

// Matches reports whether every one of the silence's matchers is present
// with the same value in labels. An empty Matchers set matches nothing —
// a silence must name what it silences.
func (s Silence) Matches(labels map[string]string) bool {
	if len(s.Matchers) == 0 {
		return false
	}
	for k, v := range s.Matchers {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// SilenceStore holds active silences (by host, rule, or arbitrary label —
// ADR-0009 doesn't distinguish these, they're all just label matchers).
type SilenceStore struct {
	mu       sync.Mutex
	silences map[string]Silence
}

// NewSilenceStore returns an empty store.
func NewSilenceStore() *SilenceStore {
	return &SilenceStore{silences: make(map[string]Silence)}
}

// Add stores sil, keyed by its ID.
func (s *SilenceStore) Add(sil Silence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.silences[sil.ID] = sil
}

// Remove deletes a silence by ID before its natural expiry.
func (s *SilenceStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.silences, id)
}

// Silenced reports whether labels are covered by any silence active at t.
func (s *SilenceStore) Silenced(t time.Time, labels map[string]string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sil := range s.silences {
		if sil.Active(t) && sil.Matches(labels) {
			return true
		}
	}
	return false
}

// Active returns every silence in effect at t, for a status/UI listing.
func (s *SilenceStore) Active(t time.Time) []Silence {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Silence
	for _, sil := range s.silences {
		if sil.Active(t) {
			out = append(out, sil)
		}
	}
	return out
}
