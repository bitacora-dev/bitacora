package faultcluster

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// MinSamples and MaxPValue are ADR-0011's own significance thresholds:
// "cuando la desviación es significativa (p < 0,01 con al menos 5
// muestras)".
const (
	MinSamples = 5
	MaxPValue  = 0.01
)

// Tracker consumes segfaults (already extracted and CPU-enriched by
// internal/extraction's kernel-segfault rule — see this package's README)
// and counts them per physical core, flagging a core whose share is
// statistically implausible under a uniform-across-active-cores null
// hypothesis.
type Tracker struct {
	topo Topology

	mu          sync.Mutex
	countByCore map[int]int
	total       int
	flagged     map[int]bool // physical core id -> already emitted hw.cpu_fault_cluster once
}

// NewTracker returns a Tracker correlating against topo.
func NewTracker(topo Topology) *Tracker {
	return &Tracker{
		topo:        topo,
		countByCore: map[int]int{},
		flagged:     map[int]bool{},
	}
}

// Observe records one segfault on logicalCPU and returns a
// hw.cpu_fault_cluster Event the first time that physical core's share
// becomes statistically significant. It never re-emits for a core once
// flagged — ADR-0011 describes detecting a cluster, not paging on every
// single subsequent fault on an already-identified bad core; that's
// alerting's job (ADR-0009), driven off this event, not this package's.
//
// logicalCPU that doesn't resolve to a known core (topology lookup miss —
// the common case when the process producing the fault has already
// exited, per ADR-0011's own "best-effort" framing) is silently ignored:
// it's not counted at all, since counting it against no core would bias
// every core's share downward without a right place to put it.
func (t *Tracker) Observe(hostID, comm string, logicalCPU int, now time.Time) *schema.Event {
	coreID, ok := t.topo.LogicalToCore[logicalCPU]
	if !ok {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.countByCore[coreID]++
	t.total++
	k := t.countByCore[coreID]

	if t.flagged[coreID] || k < MinSamples {
		return nil
	}

	activeCores := len(t.topo.ActiveCores())
	if activeCores <= 0 {
		return nil
	}
	p := 1.0 / float64(activeCores)

	pValue := upperTailPValue(t.total, k, p)
	if pValue >= MaxPValue {
		return nil
	}

	t.flagged[coreID] = true
	return t.buildEvent(hostID, comm, coreID, logicalCPU, k, t.total, pValue, now)
}

func (t *Tracker) buildEvent(hostID, comm string, coreID, logicalCPU, k, n int, pValue float64, now time.Time) *schema.Event {
	id, _ := ulid.New(ulid.Timestamp(now), ulid.Monotonic(rand.Reader, 0))

	attrs := schema.Labels{
		"comm":         comm,
		"core_id":      fmt.Sprintf("%d", coreID),
		"cpu":          fmt.Sprintf("%d", logicalCPU),
		"fault_count":  fmt.Sprintf("%d", k),
		"total_faults": fmt.Sprintf("%d", n),
		"p_value":      fmt.Sprintf("%.6f", pValue),
		"core_type":    string(t.topo.CoreType[logicalCPU]),
	}

	event := schema.Event{
		ID:       id.String(),
		TS:       now,
		HostID:   hostID,
		Source:   "faultcluster",
		Type:     "hw.cpu_fault_cluster",
		Severity: schema.SeverityWarn,
		Title:    fmt.Sprintf("%d of %d segfaults clustered on physical core %d", k, n, coreID),
		Subject:  schema.EventSubject{Kind: "cpu_core", Name: fmt.Sprintf("%d", coreID)},
		Attrs:    attrs,
		Schema:   schema.CurrentSchemaVersion,
	}
	return &event
}
