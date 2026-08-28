package schema

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MaxActiveSeriesPerHost is the cardinality budget from ADR-0006: CI must
// fail a collector that exceeds it.
const MaxActiveSeriesPerHost = 2000

// CardinalityTracker counts distinct active series per host and rejects a
// new one once a host crosses MaxActiveSeriesPerHost. A "series" is a
// metric name plus its exact label set, same as Prometheus.
type CardinalityTracker struct {
	limit int

	mu   sync.Mutex
	seen map[string]map[string]struct{} // host_id -> series key -> present
}

// NewCardinalityTracker returns a tracker enforcing limit active series per
// host. Use MaxActiveSeriesPerHost for the ADR-0006 budget.
func NewCardinalityTracker(limit int) *CardinalityTracker {
	return &CardinalityTracker{limit: limit, seen: make(map[string]map[string]struct{})}
}

// Observe registers m's series for its host. It returns an error, without
// registering the series, if this host has already reached the limit and m
// is a series not seen before for it. Re-observing an already-known series
// never fails.
func (t *CardinalityTracker) Observe(m Metric) error {
	key := seriesKey(m.Name, m.Labels)

	t.mu.Lock()
	defer t.mu.Unlock()

	hostSeries, ok := t.seen[m.HostID]
	if !ok {
		hostSeries = make(map[string]struct{})
		t.seen[m.HostID] = hostSeries
	}

	if _, known := hostSeries[key]; known {
		return nil
	}

	if len(hostSeries) >= t.limit {
		return fmt.Errorf("host %q: cardinality budget of %d active series exceeded by metric %q", m.HostID, t.limit, m.Name)
	}

	hostSeries[key] = struct{}{}
	return nil
}

// Count returns the number of distinct series currently tracked for a host.
func (t *CardinalityTracker) Count(hostID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.seen[hostID])
}

func seriesKey(name string, labels Labels) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteByte('\x1f')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}
