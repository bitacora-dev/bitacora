package faultcluster

import (
	"testing"
	"time"
)

// eightCoreTopology gives every core its own single logical CPU (0-7),
// all online — the simplest topology that still exercises "N active
// cores" as the binomial null hypothesis's denominator.
func eightCoreTopology() Topology {
	topo := Topology{
		LogicalToCore: map[int]int{},
		CoreType:      map[int]CoreType{},
		Online:        map[int]bool{},
	}
	for i := 0; i < 8; i++ {
		topo.LogicalToCore[i] = i
		topo.CoreType[i] = CoreTypeUnknown
		topo.Online[i] = true
	}
	return topo
}

func TestTracker_UniformFaultsNeverFlag(t *testing.T) {
	tracker := NewTracker(eightCoreTopology())
	now := time.Now()

	// 8 rounds, one fault per core each round: perfectly uniform.
	for round := 0; round < 8; round++ {
		for cpu := 0; cpu < 8; cpu++ {
			if event := tracker.Observe("host-a", "node", cpu, now); event != nil {
				t.Fatalf("expected no event for a uniform distribution, got %+v", event)
			}
		}
	}
}

func TestTracker_ClusteredFaultsEventuallyFlag(t *testing.T) {
	tracker := NewTracker(eightCoreTopology())
	now := time.Now()

	var got bool
	// All faults land on cpu0 (core 0) — should become significant well
	// before ADR-0011's own "34 faults, 31 on one core" example.
	for i := 0; i < 34; i++ {
		if e := tracker.Observe("host-a", "node", 0, now); e != nil {
			got = true
			if e.Type != "hw.cpu_fault_cluster" {
				t.Fatalf("expected hw.cpu_fault_cluster, got %q", e.Type)
			}
			if e.Attrs["core_id"] != "0" {
				t.Fatalf("expected core_id 0, got %q", e.Attrs["core_id"])
			}
			if e.HostID != "host-a" {
				t.Fatalf("expected host-a, got %q", e.HostID)
			}
			if err := e.Validate(); err != nil {
				t.Fatalf("expected a valid Event: %v", err)
			}
			break
		}
	}
	if !got {
		t.Fatal("expected the clustered core to eventually be flagged")
	}
}

func TestTracker_NeverFlagsBelowMinSamples(t *testing.T) {
	tracker := NewTracker(eightCoreTopology())
	now := time.Now()

	for i := 0; i < MinSamples-1; i++ {
		if e := tracker.Observe("host-a", "node", 0, now); e != nil {
			t.Fatalf("expected no event before MinSamples is reached, got one at fault %d", i+1)
		}
	}
}

func TestTracker_OnlyFlagsOncePerCore(t *testing.T) {
	tracker := NewTracker(eightCoreTopology())
	now := time.Now()

	flags := 0
	for i := 0; i < 50; i++ {
		if e := tracker.Observe("host-a", "node", 0, now); e != nil {
			flags++
		}
	}
	if flags != 1 {
		t.Fatalf("expected exactly 1 flag for the same core, got %d", flags)
	}
}

func TestTracker_UnknownCPUIsIgnoredNotCounted(t *testing.T) {
	tracker := NewTracker(eightCoreTopology())
	now := time.Now()

	if e := tracker.Observe("host-a", "node", 999, now); e != nil {
		t.Fatalf("expected nil for an unresolvable CPU, got %+v", e)
	}
	tracker.mu.Lock()
	total := tracker.total
	tracker.mu.Unlock()
	if total != 0 {
		t.Fatalf("expected an unresolvable CPU not to be counted at all, total=%d", total)
	}
}

func TestTracker_NoActiveCoresNeverPanics(t *testing.T) {
	topo := Topology{LogicalToCore: map[int]int{0: 0}, CoreType: map[int]CoreType{0: CoreTypeUnknown}, Online: map[int]bool{0: false}}
	tracker := NewTracker(topo)
	for i := 0; i < 10; i++ {
		tracker.Observe("host-a", "node", 0, time.Now())
	}
}
