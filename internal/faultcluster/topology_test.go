package faultcluster

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeSysFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// fourCPUTopology builds a fixture matching a 2-core/4-thread SMT layout:
// cpu0+cpu1 are core 0's two threads, cpu2+cpu3 are core 1's.
func fourCPUTopology(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cpu := func(id, coreID int) {
		writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu"+strconv.Itoa(id), "topology", "core_id"), strconv.Itoa(coreID))
	}
	cpu(0, 0)
	cpu(1, 0)
	cpu(2, 1)
	cpu(3, 1)
	// cpu0 has no "online" file (always on); the rest are explicitly online.
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu1", "online"), "1")
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu2", "online"), "1")
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu3", "online"), "1")
	return root
}

func TestReadTopology_MapsLogicalCPUsToPhysicalCores(t *testing.T) {
	topo, err := ReadTopology(fourCPUTopology(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.LogicalToCore[0] != 0 || topo.LogicalToCore[1] != 0 {
		t.Errorf("expected cpu0/cpu1 on core 0, got %v", topo.LogicalToCore)
	}
	if topo.LogicalToCore[2] != 1 || topo.LogicalToCore[3] != 1 {
		t.Errorf("expected cpu2/cpu3 on core 1, got %v", topo.LogicalToCore)
	}
	if len(topo.ActiveCores()) != 2 {
		t.Errorf("expected 2 active cores, got %d: %v", len(topo.ActiveCores()), topo.ActiveCores())
	}
}

func TestReadTopology_MissingCoreIDMeansEachCPUIsItsOwnCore(t *testing.T) {
	root := t.TempDir()
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu0", "online"), "1")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.LogicalToCore[0] != 0 {
		t.Errorf("expected cpu0 to map to core 0 (itself), got %d", topo.LogicalToCore[0])
	}
}

func TestReadTopology_OfflineCPUsDetected(t *testing.T) {
	root := fourCPUTopology(t)
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu2", "online"), "0")
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu3", "online"), "0")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	offline := topo.OfflineCPUs()
	if len(offline) != 2 || offline[0] != 2 || offline[1] != 3 {
		t.Fatalf("expected [2 3] offline, got %v", offline)
	}
	// Core 1 has no online CPU left — it drops out of ActiveCores.
	if len(topo.ActiveCores()) != 1 {
		t.Fatalf("expected 1 active core after offlining core 1's threads, got %d", len(topo.ActiveCores()))
	}
}

func TestReadTopology_HybridPMUClassifiesCoreType(t *testing.T) {
	root := fourCPUTopology(t)
	writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_core", "cpus"), "0-1\n")
	writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_atom", "cpus"), "2-3\n")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.CoreType[0] != CoreTypeP || topo.CoreType[1] != CoreTypeP {
		t.Errorf("expected cpu0/cpu1 as P-core, got %v/%v", topo.CoreType[0], topo.CoreType[1])
	}
	if topo.CoreType[2] != CoreTypeE || topo.CoreType[3] != CoreTypeE {
		t.Errorf("expected cpu2/cpu3 as E-core, got %v/%v", topo.CoreType[2], topo.CoreType[3])
	}
}

func TestReadTopology_NoHybridPMUMeansUnknownCoreType(t *testing.T) {
	topo, err := ReadTopology(fourCPUTopology(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.CoreType[0] != CoreTypeUnknown {
		t.Errorf("expected CoreTypeUnknown without a hybrid PMU device, got %v", topo.CoreType[0])
	}
}

func TestReadTopology_UsesAuthoritativeIsolatedCPUList(t *testing.T) {
	root := fourCPUTopology(t)
	cpuRoot := filepath.Join(root, "devices", "system", "cpu")
	writeSysFile(t, filepath.Join(cpuRoot, "isolated"), "1-2, 4\n")
	// These kernel tuning lists are corroborating context only. cpu3 must not
	// be marked isolated unless it appears in the authoritative isolated list.
	writeSysFile(t, filepath.Join(cpuRoot, "nohz_full"), "3\n")
	writeSysFile(t, filepath.Join(cpuRoot, "rcu_nocbs"), "3\n")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !topo.IsolatedAvailable {
		t.Fatal("expected authoritative isolated CPU list to be available")
	}
	if !topo.Isolated[1] || !topo.Isolated[2] {
		t.Fatalf("expected CPUs 1 and 2 isolated, got %v", topo.Isolated)
	}
	if topo.Isolated[0] || topo.Isolated[3] || topo.Isolated[4] {
		t.Fatalf("expected only topology CPUs 1 and 2 isolated, got %v", topo.Isolated)
	}
}

func TestReadTopology_MissingIsolatedCPUListIsUnavailable(t *testing.T) {
	root := fourCPUTopology(t)
	cpuRoot := filepath.Join(root, "devices", "system", "cpu")
	writeSysFile(t, filepath.Join(cpuRoot, "nohz_full"), "1\n")
	writeSysFile(t, filepath.Join(cpuRoot, "rcu_nocbs"), "2\n")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.IsolatedAvailable {
		t.Fatal("expected missing isolated CPU list to be unavailable")
	}
	if len(topo.Isolated) != 0 {
		t.Fatalf("expected no isolated CPU values without the authoritative list, got %v", topo.Isolated)
	}
}

func TestReadTopology_EmptyIsolatedCPUListIsAvailable(t *testing.T) {
	root := fourCPUTopology(t)
	writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "isolated"), "\n")

	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !topo.IsolatedAvailable {
		t.Fatal("expected an empty isolated CPU list to be available")
	}
	if len(topo.Isolated) != 0 {
		t.Fatalf("expected an empty isolated CPU list, got %v", topo.Isolated)
	}
}

func TestParseCPUList(t *testing.T) {
	cases := map[string][]int{
		"0-3":     {0, 1, 2, 3},
		"0,2,4":   {0, 2, 4},
		"0-1,5-6": {0, 1, 5, 6},
		"":        {},
		"3":       {3},
	}
	for input, want := range cases {
		got := parseCPUList(input)
		if len(got) != len(want) {
			t.Errorf("parseCPUList(%q) = %v, want %v", input, got, want)
			continue
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("parseCPUList(%q): expected %d present, got %v", input, w, got)
			}
		}
	}
}

// raptorLakeTopology reproduces the sysfs layout of the i9-13900K that
// motivates ADR-0011: cpu0..cpu15 are eight SMT P-cores (core_id 0,4,8,...,28)
// and cpu16..cpu31 are sixteen single-thread E-cores (core_id 32..47). offline
// names the logical CPUs the kernel has taken down, which keep their cpuN
// directory and their "online" file but lose the whole topology/ subtree.
func raptorLakeTopology(t *testing.T, offline ...int) string {
	t.Helper()
	root := t.TempDir()
	cpuRoot := filepath.Join(root, "devices", "system", "cpu")
	down := map[int]bool{}
	for _, cpu := range offline {
		down[cpu] = true
	}

	var pCores, eCores []string
	for cpu := 0; cpu < 32; cpu++ {
		dir := filepath.Join(cpuRoot, "cpu"+strconv.Itoa(cpu))
		coreID := 4 * (cpu / 2)
		if cpu >= 16 {
			coreID = 16 + cpu
		}
		if !down[cpu] {
			writeSysFile(t, filepath.Join(dir, "topology", "core_id"), strconv.Itoa(coreID)+"\n")
			if cpu < 16 {
				pCores = append(pCores, strconv.Itoa(cpu))
			} else {
				eCores = append(eCores, strconv.Itoa(cpu))
			}
		}
		if cpu > 0 {
			online := "1"
			if down[cpu] {
				online = "0"
			}
			writeSysFile(t, filepath.Join(dir, "online"), online+"\n")
		}
	}

	// An offline CPU is absent from the hybrid PMU lists too, exactly like the
	// kernel reports it: cpu_core reads "0-7,10-15" with cpu8/cpu9 down.
	writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_core", "cpus"), strings.Join(pCores, ",")+"\n")
	writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_atom", "cpus"), strings.Join(eCores, ",")+"\n")
	return root
}

func TestReadTopology_OfflineCoreKeepsItsThreadsOffOtherCores(t *testing.T) {
	topo, err := ReadTopology(raptorLakeTopology(t, 8, 9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The whole point: cpu8's own number is cpu4/cpu5's real core_id, so the
	// old "an unreadable CPU is its own core" fallback merged a dead thread
	// into a healthy core.
	if got := topo.LogicalToCore[8]; got != 16 {
		t.Errorf("expected offline cpu8 on its real core 16, got %d", got)
	}
	if got := topo.LogicalToCore[9]; got != 16 {
		t.Errorf("expected offline cpu9 on its real core 16, got %d", got)
	}
	if topo.LogicalToCore[4] != 8 || topo.LogicalToCore[5] != 8 {
		t.Errorf("expected cpu4/cpu5 to stay on core 8, got %d/%d", topo.LogicalToCore[4], topo.LogicalToCore[5])
	}
	if !topo.CoreIDInferred[8] || !topo.CoreIDInferred[9] {
		t.Error("expected the reconstructed cores of cpu8/cpu9 to be marked inferred")
	}
	if topo.CoreIDInferred[4] || topo.CoreIDInferred[5] {
		t.Error("expected the readable cores of cpu4/cpu5 not to be marked inferred")
	}

	// cpu4 and cpu5 are still running, so their core must still be active.
	if !topo.ActiveCores()[8] {
		t.Error("expected core 8 to stay active with cpu4/cpu5 online")
	}
	if topo.ActiveCores()[16] {
		t.Error("expected core 16 inactive with both its threads offline")
	}
	if len(topo.ActiveCores()) != 23 {
		t.Errorf("expected 23 active cores (7 P + 16 E), got %d", len(topo.ActiveCores()))
	}
}

func TestReadTopology_OfflineCPUInheritsItsCoreType(t *testing.T) {
	topo, err := ReadTopology(raptorLakeTopology(t, 8, 9))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The hybrid PMU lists cannot classify an offline CPU, but both readable
	// neighbours of the gap are P-cores, so the core between them is one too.
	if topo.CoreType[8] != CoreTypeP || topo.CoreType[9] != CoreTypeP {
		t.Errorf("expected cpu8/cpu9 as P-core, got %v/%v", topo.CoreType[8], topo.CoreType[9])
	}
}

func TestReadTopology_OfflineThreadRejoinsItsRunningCore(t *testing.T) {
	topo, err := ReadTopology(raptorLakeTopology(t, 5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// cpu4 is still readable on core 8 and brackets the gap on both sides
	// together with cpu6, so cpu5 belongs to core 8 — not to a core of its own.
	if got := topo.LogicalToCore[5]; got != 8 {
		t.Errorf("expected offline cpu5 to stay on core 8 with its sibling, got %d", got)
	}
	if !topo.ActiveCores()[8] {
		t.Error("expected core 8 to stay active with cpu4 online")
	}
}

func TestReadTopology_OfflineSingleThreadCoreIsReconstructed(t *testing.T) {
	topo, err := ReadTopology(raptorLakeTopology(t, 20))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// E-cores have one thread each and a step of 1, so cpu20 is core 36.
	if got := topo.LogicalToCore[20]; got != 36 {
		t.Errorf("expected offline cpu20 on core 36, got %d", got)
	}
	if topo.ActiveCores()[36] {
		t.Error("expected core 36 inactive with its only thread offline")
	}
}

func TestReadTopology_UnbracketedOfflineCPUGetsACoreOfItsOwn(t *testing.T) {
	// The tail of the CPU range is offline, so there is no readable neighbour
	// above the gap to interpolate towards. The result must still not be a
	// core id any running CPU reported.
	topo, err := ReadTopology(raptorLakeTopology(t, 30, 31))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	readable := map[int]bool{}
	for cpu := 0; cpu < 30; cpu++ {
		readable[topo.LogicalToCore[cpu]] = true
	}
	for _, cpu := range []int{30, 31} {
		if readable[topo.LogicalToCore[cpu]] {
			t.Errorf("cpu%d was given core %d, which a running CPU already reports", cpu, topo.LogicalToCore[cpu])
		}
		if !topo.CoreIDInferred[cpu] {
			t.Errorf("expected cpu%d to be marked inferred", cpu)
		}
	}
	if topo.LogicalToCore[30] == topo.LogicalToCore[31] {
		t.Error("expected unreconstructable CPUs to claim no sibling relationship")
	}
}

func TestReadTopology_OfflineCPUNeverCollidesWithARunningCore(t *testing.T) {
	// Every single logical CPU number on this machine is some other core's
	// real core_id for cpu0..cpu7, so an offline CPU anywhere in that range
	// used to be able to corrupt a running core. None may.
	for _, offline := range [][]int{{8, 9}, {2, 3}, {4}, {12, 13}, {16}, {8, 9, 12, 13}} {
		topo, err := ReadTopology(raptorLakeTopology(t, offline...))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		down := map[int]bool{}
		for _, cpu := range offline {
			down[cpu] = true
		}
		for _, cpu := range offline {
			core := topo.LogicalToCore[cpu]
			for other := 0; other < 32; other++ {
				if down[other] || topo.LogicalToCore[other] != core {
					continue
				}
				// Sharing a core with a running CPU is only legitimate when
				// that CPU is a real sibling thread of the same physical core.
				if other/2 != cpu/2 || cpu >= 16 {
					t.Errorf("offline %v: cpu%d landed on core %d, shared with running cpu%d", offline, cpu, core, other)
				}
			}
		}
	}
}
