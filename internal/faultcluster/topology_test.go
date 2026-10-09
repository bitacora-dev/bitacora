package faultcluster

import (
	"fmt"
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
		if !topo.CoreIDUnknown[cpu] || topo.CoreIDInferred[cpu] {
			t.Errorf("expected cpu%d to be marked unknown, not inferred", cpu)
		}
	}
	if topo.LogicalToCore[30] == topo.LogicalToCore[31] {
		t.Error("expected unreconstructable CPUs to claim no sibling relationship")
	}
}

// sysCPU describes one logical CPU of a fixture: its real physical core and,
// on a hybrid part with PMU lists, its core type.
type sysCPU struct {
	core     int
	coreType CoreType
}

// sysTopology writes a sysfs fixture from a ground-truth layout. offline CPUs
// keep their cpuN directory and "online" file but lose topology/, and are
// left out of the hybrid PMU lists, exactly like the kernel reports them.
// pmu=false omits those lists entirely, as on a kernel or CPU without them.
func sysTopology(t *testing.T, layout []sysCPU, pmu bool, offline ...int) string {
	t.Helper()
	root := t.TempDir()
	cpuRoot := filepath.Join(root, "devices", "system", "cpu")
	down := map[int]bool{}
	for _, cpu := range offline {
		down[cpu] = true
	}
	var pCores, eCores []string
	for cpu, info := range layout {
		dir := filepath.Join(cpuRoot, "cpu"+strconv.Itoa(cpu))
		if !down[cpu] {
			writeSysFile(t, filepath.Join(dir, "topology", "core_id"), strconv.Itoa(info.core)+"\n")
			switch info.coreType {
			case CoreTypeP:
				pCores = append(pCores, strconv.Itoa(cpu))
			case CoreTypeE:
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
	if pmu {
		writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_core", "cpus"), strings.Join(pCores, ",")+"\n")
		writeSysFile(t, filepath.Join(root, "bus", "event_source", "devices", "cpu_atom", "cpus"), strings.Join(eCores, ",")+"\n")
	}
	return root
}

// raptorLakeLayout: cpu0..cpu15 are eight SMT P-cores (core_id 0,4,...,28),
// cpu16..cpu31 sixteen single-thread E-cores (core_id 32..47).
func raptorLakeLayout() []sysCPU {
	layout := make([]sysCPU, 32)
	for cpu := range layout {
		if cpu < 16 {
			layout[cpu] = sysCPU{core: 4 * (cpu / 2), coreType: CoreTypeP}
		} else {
			layout[cpu] = sysCPU{core: 16 + cpu, coreType: CoreTypeE}
		}
	}
	return layout
}

// interleavedLayout: cores siblings on cpu k and cpu k+cores, the numbering
// of AMD and most non-hybrid Intel parts. coreStep spaces the core ids.
func interleavedLayout(cores, coreStep int) []sysCPU {
	layout := make([]sysCPU, 2*cores)
	for cpu := range layout {
		layout[cpu] = sysCPU{core: coreStep * (cpu % cores), coreType: CoreTypeUnknown}
	}
	return layout
}

// singleThreadLayout: one thread per core, consecutive core ids — SMT off on
// an AMD part once the upper half of the CPUs is gone, or a non-SMT CPU.
func singleThreadLayout(cores int) []sysCPU {
	layout := make([]sysCPU, cores)
	for cpu := range layout {
		layout[cpu] = sysCPU{core: cpu, coreType: CoreTypeUnknown}
	}
	return layout
}

type topologyFixture struct {
	name   string
	layout []sysCPU
	pmu    bool
	// baseOffline is always offline on top of the CPUs under test, e.g. the
	// siblings SMT-off (nosmt) takes down.
	baseOffline []int
}

func topologyFixtures() []topologyFixture {
	consecutiveNoSMT := make([]sysCPU, 16)
	var oddCPUs []int
	for cpu := range consecutiveNoSMT {
		consecutiveNoSMT[cpu] = sysCPU{core: 4 * (cpu / 2), coreType: CoreTypeUnknown}
		if cpu%2 == 1 {
			oddCPUs = append(oddCPUs, cpu)
		}
	}
	return []topologyFixture{
		{name: "raptor lake with PMU lists", layout: raptorLakeLayout(), pmu: true},
		{name: "hybrid without PMU lists", layout: raptorLakeLayout(), pmu: false},
		{name: "AMD k/k+N/2 siblings", layout: interleavedLayout(8, 1)},
		{name: "non-hybrid Intel k/k+N/2 siblings, spaced core ids", layout: interleavedLayout(4, 4)},
		{name: "AMD with SMT off", layout: interleavedLayout(8, 1), baseOffline: []int{8, 9, 10, 11, 12, 13, 14, 15}},
		{name: "consecutive siblings with SMT off", layout: consecutiveNoSMT, baseOffline: oddCPUs},
		{name: "no SMT", layout: singleThreadLayout(8)},
	}
}

// checkTopologyAgainstTruth asserts the invariants that make a reconstructed
// topology safe to draw and to correlate against:
//   - a CPU marked inferred sits on its real core;
//   - every offline CPU is either inferred or unknown, never silently both
//     or neither;
//   - no CPU ever shares a core with a CPU that is not its real sibling;
//   - an unknown CPU shares its placeholder with nobody.
func checkTopologyAgainstTruth(t *testing.T, label string, layout []sysCPU, down map[int]bool, topo Topology) {
	t.Helper()
	for cpu, info := range layout {
		got, ok := topo.LogicalToCore[cpu]
		if !ok {
			t.Errorf("%s: cpu%d has no core at all", label, cpu)
			continue
		}
		if !down[cpu] {
			if got != info.core || topo.CoreIDInferred[cpu] || topo.CoreIDUnknown[cpu] {
				t.Errorf("%s: readable cpu%d changed: core %d (want %d), inferred=%v unknown=%v", label, cpu, got, info.core, topo.CoreIDInferred[cpu], topo.CoreIDUnknown[cpu])
			}
			continue
		}
		if topo.CoreIDInferred[cpu] == topo.CoreIDUnknown[cpu] {
			t.Errorf("%s: offline cpu%d must be exactly one of inferred/unknown, got inferred=%v unknown=%v", label, cpu, topo.CoreIDInferred[cpu], topo.CoreIDUnknown[cpu])
		}
		if topo.CoreIDInferred[cpu] && got != info.core {
			t.Errorf("%s: offline cpu%d inferred on core %d, really on %d", label, cpu, got, info.core)
		}
		for other, otherInfo := range layout {
			if other == cpu || topo.LogicalToCore[other] != got {
				continue
			}
			if topo.CoreIDUnknown[cpu] {
				t.Errorf("%s: unknown cpu%d shares its placeholder core %d with cpu%d", label, cpu, got, other)
			} else if otherInfo.core != info.core {
				t.Errorf("%s: offline cpu%d landed on core %d with cpu%d, which is not its sibling", label, cpu, got, other)
			}
		}
	}
}

func TestReadTopology_OfflineCPUNeverCollidesWithARunningCore(t *testing.T) {
	// Every single CPU and every pair of adjacent CPUs offline, on every
	// fixture layout: no reconstruction may ever put a CPU on a core it does
	// not belong to, and what it does reconstruct must be the real core.
	for _, fixture := range topologyFixtures() {
		// cpu0 is never offlined: the kernel does not allow it, and it has no
		// "online" file to say so.
		var sets [][]int
		for cpu := 1; cpu < len(fixture.layout); cpu++ {
			sets = append(sets, []int{cpu})
			if cpu+1 < len(fixture.layout) {
				sets = append(sets, []int{cpu, cpu + 1})
			}
		}
		sets = append(sets, []int{8, 9, 12, 13}, []int{5, 9}, []int{3, 4})
		for _, set := range sets {
			down := map[int]bool{}
			var offline []int
			for _, cpu := range append(append([]int{}, fixture.baseOffline...), set...) {
				if cpu < len(fixture.layout) && !down[cpu] {
					down[cpu] = true
					offline = append(offline, cpu)
				}
			}
			topo, err := ReadTopology(sysTopology(t, fixture.layout, fixture.pmu, offline...))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			checkTopologyAgainstTruth(t, fmt.Sprintf("%s, offline %v", fixture.name, offline), fixture.layout, down, topo)
		}
	}
}

func TestReadTopology_InterleavedSiblingsAreNeverReconstructed(t *testing.T) {
	// AMD: cpu3's sibling is cpu11. A consecutive-sibling reading would top
	// up core 2 or core 4 with it; the numbering disproves that, so cpu3
	// must stand alone as unknown.
	for _, fixture := range []topologyFixture{
		{name: "AMD", layout: interleavedLayout(8, 1)},
		{name: "non-hybrid Intel", layout: interleavedLayout(4, 4)},
	} {
		topo, err := ReadTopology(sysTopology(t, fixture.layout, false, 3))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if topo.CoreIDInferred[3] || !topo.CoreIDUnknown[3] {
			t.Errorf("%s: expected cpu3 unknown, got core %d inferred=%v", fixture.name, topo.LogicalToCore[3], topo.CoreIDInferred[3])
		}
	}
}

func TestReadTopology_SMTOffReconstructsOnlyWholeSingleThreadCores(t *testing.T) {
	// SMT off on AMD: cpu8..15 are the downed siblings and cpu0..7 run one
	// thread per core. cpu3 going down as well is a whole core, core 3.
	offline := []int{3, 8, 9, 10, 11, 12, 13, 14, 15}
	topo, err := ReadTopology(sysTopology(t, interleavedLayout(8, 1), false, offline...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !topo.CoreIDInferred[3] || topo.LogicalToCore[3] != 3 {
		t.Errorf("expected cpu3 inferred on core 3, got %d inferred=%v", topo.LogicalToCore[3], topo.CoreIDInferred[3])
	}
	for _, cpu := range offline[1:] {
		if !topo.CoreIDUnknown[cpu] {
			t.Errorf("expected unbracketed cpu%d unknown, got core %d", cpu, topo.LogicalToCore[cpu])
		}
	}
}

func TestReadTopology_HybridWithoutPMUListsDoesNotMergeECores(t *testing.T) {
	// Without cpu_core/cpu_atom every CPU is CoreTypeUnknown, so P-cores
	// (two threads) and E-cores (one) look like one type. Taking the larger
	// count would top up E-core 35 with cpu20; the counts disagree, so
	// nothing of that type is reconstructed.
	topo, err := ReadTopology(sysTopology(t, raptorLakeLayout(), false, 20))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topo.CoreIDInferred[20] || !topo.CoreIDUnknown[20] {
		t.Errorf("expected cpu20 unknown, got core %d inferred=%v", topo.LogicalToCore[20], topo.CoreIDInferred[20])
	}
	if topo.LogicalToCore[19] != 35 || topo.LogicalToCore[21] != 37 {
		t.Errorf("expected E-cores 35/37 untouched, got %d/%d", topo.LogicalToCore[19], topo.LogicalToCore[21])
	}
}

func TestReadTopology_WholePCoreOfflineAtTheHybridEdge(t *testing.T) {
	// P-core 7 (cpu14/cpu15, core_id 28) offline right before the first
	// E-core: the run is exactly one P-core long, the P-core below it is
	// complete, and E-core 32 sits one P step past 28. One core, not two.
	topo, err := ReadTopology(raptorLakeTopology(t, 14, 15))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, cpu := range []int{14, 15} {
		if !topo.CoreIDInferred[cpu] || topo.LogicalToCore[cpu] != 28 {
			t.Errorf("expected cpu%d inferred on core 28, got %d inferred=%v", cpu, topo.LogicalToCore[cpu], topo.CoreIDInferred[cpu])
		}
		if topo.CoreType[cpu] != CoreTypeP {
			t.Errorf("expected cpu%d as P-core, got %v", cpu, topo.CoreType[cpu])
		}
	}
}

func TestReadTopology_TwoECoresOfflineAtTheHybridEdgeStayUnknown(t *testing.T) {
	// cpu16/cpu17 are two E-cores, but the run is also one P-core long. The
	// E-core above (core 34) is not one P step past 32, so the P reading is
	// refuted and both stay unknown rather than merged.
	topo, err := ReadTopology(raptorLakeTopology(t, 16, 17))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, cpu := range []int{16, 17} {
		if !topo.CoreIDUnknown[cpu] {
			t.Errorf("expected cpu%d unknown, got core %d inferred=%v", cpu, topo.LogicalToCore[cpu], topo.CoreIDInferred[cpu])
		}
	}
	if topo.LogicalToCore[16] == topo.LogicalToCore[17] {
		t.Error("expected the two E-cores not to be merged")
	}
}

func TestReadTopology_TwoOfflineCPUsOnDifferentCores(t *testing.T) {
	cases := map[string]struct {
		offline []int
		want    map[int]int
	}{
		"apart":    {offline: []int{5, 9}, want: map[int]int{5: 8, 9: 16}},
		"adjacent": {offline: []int{3, 4}, want: map[int]int{3: 4, 4: 8}},
	}
	for name, tc := range cases {
		topo, err := ReadTopology(raptorLakeTopology(t, tc.offline...))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for cpu, core := range tc.want {
			if !topo.CoreIDInferred[cpu] || topo.LogicalToCore[cpu] != core {
				t.Errorf("%s: expected cpu%d inferred on core %d, got %d inferred=%v", name, cpu, core, topo.LogicalToCore[cpu], topo.CoreIDInferred[cpu])
			}
		}
	}
}

func TestReadTopology_OnlineCPUWithoutTopologyIsNotInferred(t *testing.T) {
	// A VM with no topology/ anywhere: every CPU is online and is its own
	// core, as before reconstruction existed. Nothing was reconstructed.
	root := t.TempDir()
	for cpu := 0; cpu < 4; cpu++ {
		writeSysFile(t, filepath.Join(root, "devices", "system", "cpu", "cpu"+strconv.Itoa(cpu), "online"), "1\n")
	}
	topo, err := ReadTopology(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for cpu := 0; cpu < 4; cpu++ {
		if topo.LogicalToCore[cpu] != cpu || topo.CoreIDInferred[cpu] || topo.CoreIDUnknown[cpu] {
			t.Errorf("expected cpu%d on core %d and unflagged, got %d inferred=%v unknown=%v", cpu, cpu, topo.LogicalToCore[cpu], topo.CoreIDInferred[cpu], topo.CoreIDUnknown[cpu])
		}
	}
}
