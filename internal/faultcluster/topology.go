// Package faultcluster implements ADR-0011's segfault↔CPU-topology
// correlation: turning "hay segfaults sueltos" into "34 segfaults en 6
// días, 31 de ellos en el core físico 4; la probabilidad de que sea azar
// es del 0,0001%" — automating exactly the reasoning that diagnosed the
// incident that motivates this ADR by hand.
package faultcluster

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CoreType distinguishes a hybrid CPU's performance and efficiency cores
// (ADR-0011's own incident involved a P-core on an i9-13900K).
type CoreType string

const (
	CoreTypeP       CoreType = "p-core"
	CoreTypeE       CoreType = "e-core"
	CoreTypeUnknown CoreType = "unknown"
)

// Topology maps logical CPUs to their physical core, online state, isolation
// state, and — best-effort — hybrid core type.
type Topology struct {
	LogicalToCore map[int]int
	// CoreIDInferred marks logical CPUs whose physical core was reconstructed
	// instead of read. Linux deletes the whole topology/ directory of an
	// offline CPU, so a CPU offlined at boot never exposes its core_id and its
	// membership has to be deduced from the CPUs that still do.
	CoreIDInferred map[int]bool
	CoreType       map[int]CoreType
	Online         map[int]bool
	Isolated       map[int]bool
	// IsolatedAvailable reports whether the authoritative isolated CPU list
	// was readable. A missing list is not evidence that every CPU is shared.
	IsolatedAvailable bool
}

// ReadTopology reads /sys/devices/system/cpu (physical core mapping and
// online state) and, when present, the hybrid-CPU PMU device lists at
// /sys/bus/event_source/devices/{cpu_core,cpu_atom}/cpus (P-core/E-core
// classification). The latter only exists on Intel hybrid CPUs running a
// kernel new enough to expose it — its absence isn't an error, every
// logical CPU is simply CoreTypeUnknown.
//
// A CPU the kernel has taken offline keeps its cpuN directory but loses the
// whole topology/ subtree, so its core_id is unreadable. Those CPUs get their
// membership reconstructed by reconstructCores instead of being treated as a
// core of their own, which would silently merge them into whichever real core
// happens to carry that number as its core_id.
func ReadTopology(sysRoot string) (Topology, error) {
	cpuRoot := filepath.Join(sysRoot, "devices", "system", "cpu")
	entries, err := os.ReadDir(cpuRoot)
	if err != nil {
		return Topology{}, err
	}

	topo := Topology{
		LogicalToCore:  map[int]int{},
		CoreIDInferred: map[int]bool{},
		CoreType:       map[int]CoreType{},
		Online:         map[int]bool{},
		Isolated:       map[int]bool{},
	}

	var present, unreadable []int
	for _, e := range entries {
		id, ok := parseCPUDirName(e.Name())
		if !ok {
			continue
		}
		present = append(present, id)
		topo.CoreType[id] = CoreTypeUnknown

		read := false
		if raw, err := os.ReadFile(filepath.Join(cpuRoot, e.Name(), "topology", "core_id")); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				topo.LogicalToCore[id] = n
				read = true
			}
		}
		if !read {
			unreadable = append(unreadable, id)
		}

		// cpu0 (and any CPU that can't be offlined) has no "online" file
		// at all — its absence means "always online", not "unknown".
		online := true
		if raw, err := os.ReadFile(filepath.Join(cpuRoot, e.Name(), "online")); err == nil {
			online = strings.TrimSpace(string(raw)) == "1"
		}
		topo.Online[id] = online
	}
	sort.Ints(present)
	sort.Ints(unreadable)

	for id := range hybridCPUSet(sysRoot, "cpu_core") {
		if _, ok := topo.Online[id]; ok {
			topo.CoreType[id] = CoreTypeP
		}
	}
	for id := range hybridCPUSet(sysRoot, "cpu_atom") {
		if _, ok := topo.Online[id]; ok {
			topo.CoreType[id] = CoreTypeE
		}
	}

	reconstructCores(&topo, present, unreadable)

	isolated, isolatedAvailable := isolatedCPUSet(sysRoot)
	topo.IsolatedAvailable = isolatedAvailable
	for id := range isolated {
		if _, ok := topo.Online[id]; ok {
			topo.Isolated[id] = true
		}
	}

	return topo, nil
}

// reconstructCores fills in LogicalToCore for every CPU whose core_id was
// unreadable, and marks each one in CoreIDInferred.
//
// The one thing it must never do is hand such a CPU a core id that belongs to
// a different physical core: that is what made offlining cpu8/cpu9 (the two
// threads of a degraded P-core on an i9-13900K) drag the healthy cpu4/cpu5
// into an "offline" core, because those two really do sit on core_id 8.
//
// Order of preference, most to least evidence:
//  1. fillTopologyGaps, which extrapolates the kernel's own enumeration from
//     the CPUs that are readable.
//  2. the CPU's own number, but only on a machine that publishes no core id
//     at all. A kernel with no topology/ subtree anywhere is one that does not
//     publish SMT information, so every logical CPU really is its own core and
//     there is no core identity left to corrupt.
//  3. a core id past every readable one, i.e. "a core of its own, siblings
//     unknown". Deliberately useless for correlation, but it cannot corrupt a
//     core that is actually running.
func reconstructCores(topo *Topology, present, unreadable []int) {
	if len(unreadable) == 0 {
		return
	}

	readableCores := map[int]bool{}
	maxReadableCore := 0
	for _, cpu := range present {
		if core, ok := topo.LogicalToCore[cpu]; ok {
			readableCores[core] = true
			if core > maxReadableCore {
				maxReadableCore = core
			}
		}
	}

	inferred := fillTopologyGaps(topo, present, unreadable, readableCores)
	next := maxReadableCore + 1
	for _, cpu := range unreadable {
		topo.CoreIDInferred[cpu] = true
		if core, ok := inferred[cpu]; ok {
			topo.LogicalToCore[cpu] = core
			continue
		}
		switch {
		case len(readableCores) == 0:
			topo.LogicalToCore[cpu] = cpu
		default:
			topo.LogicalToCore[cpu] = next
			next++
		}
	}
}

// fillTopologyGaps reconstructs the core of unreadable CPUs by extrapolating
// the enumeration the kernel used for the readable ones. Linux numbers the
// sibling threads of a core consecutively and walks core ids in a constant
// step, so the readable P-cores of an i9-13900K with cpu8/cpu9 offlined
// (core_id 0,4,8,12 for cpu0..cpu7 then 20,24,28 for cpu10..cpu15) pin the
// step at 4 and leave exactly one core id, 16, unaccounted for between cpu7
// and cpu10 — the real core of the two missing threads.
//
// Each gap is reconstructed only from its own two readable neighbours: the
// missing CPUs first top up the core below to its full thread count, then fill
// whole cores hidden between the two, then top up the core above. A gap that
// is unbracketed, straddles the P-core/E-core boundary, or whose length does
// not match that accounting exactly is left out of the result for the caller
// to fail closed on — a wrong sibling set is worse than an unknown one.
//
// A reconstructed CPU also takes the hybrid core type of the two neighbours it
// was placed between, which the PMU device lists cannot give it: they only
// list CPUs that are online.
func fillTopologyGaps(topo *Topology, present, unreadable []int, readableCores map[int]bool) map[int]int {
	filled := map[int]int{}
	missing := map[int]bool{}
	for _, cpu := range unreadable {
		missing[cpu] = true
	}
	steps := coreIDSteps(topo, present, missing)
	threadsPerCore, coreThreads := readableThreadCounts(topo, present, missing)

	for i := 0; i < len(unreadable); i++ {
		run := []int{unreadable[i]}
		for i+1 < len(unreadable) && unreadable[i+1] == unreadable[i]+1 {
			i++
			run = append(run, unreadable[i])
		}

		low, lowOK := topo.LogicalToCore[run[0]-1]
		high, highOK := topo.LogicalToCore[run[len(run)-1]+1]
		if !lowOK || !highOK {
			continue // unbracketed: nothing to interpolate between
		}
		coreType := topo.CoreType[run[0]-1]
		if coreType != topo.CoreType[run[len(run)-1]+1] {
			continue // the gap straddles a hybrid boundary
		}
		threads, ok := threadsPerCore[coreType]
		if !ok || threads <= 0 {
			continue
		}

		step := steps[coreType]
		cores, ok := assignGap(run, low, high, threads, coreThreads, step, readableCores, filled)
		if !ok {
			for _, cpu := range run {
				delete(filled, cpu)
			}
			continue
		}
		for _, cpu := range run {
			topo.CoreType[cpu] = coreType
		}
		for _, core := range cores {
			coreThreads[core]++
		}
	}
	return filled
}

// assignGap spreads one run of unreadable CPUs over the core below it, the
// cores hidden inside it, and the core above it, reporting false when the run
// length does not match that accounting exactly or when a reconstructed core
// would collide with one a readable CPU already claims.
//
// Naming a core no readable CPU reports takes the strongest evidence: the run
// has to leave a whole number of cores unaccounted for, and the step between
// core ids has to have been measured somewhere the gap did not touch, and the
// distance between the two neighbours has to be exactly that many steps.
// Topping up the neighbours' own thread counts needs none of that, because it
// invents no core id at all.
func assignGap(run []int, low, high, threads int, coreThreads map[int]int, step int, readableCores map[int]bool, filled map[int]int) ([]int, bool) {
	assigned := make([]int, 0, len(run))
	offset := 0
	place := func(count, core int) bool {
		if core != low && core != high && readableCores[core] {
			return false // a core no readable CPU claims is the only one free
		}
		for n := 0; n < count; n++ {
			filled[run[offset]] = core
			assigned = append(assigned, core)
			offset++
		}
		return true
	}

	missingLow := max(0, threads-coreThreads[low])
	if low == high {
		// The gap sits between two threads of one readable core.
		if len(run) != missingLow || !place(missingLow, low) {
			return nil, false
		}
		return assigned, true
	}

	missingHigh := max(0, threads-coreThreads[high])
	remaining := len(run) - missingLow - missingHigh
	if remaining < 0 || remaining%threads != 0 {
		return nil, false
	}
	hidden := remaining / threads
	if hidden > 0 && (step <= 0 || high-low != step*(hidden+1)) {
		return nil, false
	}

	if !place(missingLow, low) {
		return nil, false
	}
	for n := 0; n < hidden; n++ {
		if !place(threads, low+step*(n+1)) {
			return nil, false
		}
	}
	if !place(missingHigh, high) {
		return nil, false
	}
	return assigned, true
}

// readableThreadCounts returns how many readable logical CPUs each core has,
// and per core type the largest of those counts — the machine's own answer to
// "how many threads does a core of this type have", which is what says whether
// a gap next to a core is one of its missing threads or a core of its own.
func readableThreadCounts(topo *Topology, present []int, missing map[int]bool) (map[CoreType]int, map[int]int) {
	coreThreads := map[int]int{}
	for _, cpu := range present {
		if missing[cpu] {
			continue
		}
		if core, ok := topo.LogicalToCore[cpu]; ok {
			coreThreads[core]++
		}
	}
	threadsPerCore := map[CoreType]int{}
	for _, cpu := range present {
		if missing[cpu] {
			continue
		}
		core, ok := topo.LogicalToCore[cpu]
		if !ok {
			continue
		}
		if coreType := topo.CoreType[cpu]; coreThreads[core] > threadsPerCore[coreType] {
			threadsPerCore[coreType] = coreThreads[core]
		}
	}
	return threadsPerCore, coreThreads
}

// coreIDSteps returns, per core type, the constant distance between the core
// ids of two adjacent cores — measured only across consecutive CPU numbers
// that are both readable, so a gap never contributes to the step it is about
// to be measured against. A core type whose observed distances disagree, or
// that offers none at all, is absent from the result.
func coreIDSteps(topo *Topology, present []int, missing map[int]bool) map[CoreType]int {
	steps := map[CoreType]int{}
	rejected := map[CoreType]bool{}
	for _, cpu := range present {
		next := cpu + 1
		if missing[cpu] || missing[next] {
			continue
		}
		low, lowOK := topo.LogicalToCore[cpu]
		high, highOK := topo.LogicalToCore[next]
		if !lowOK || !highOK || low == high {
			continue
		}
		coreType := topo.CoreType[cpu]
		if coreType != topo.CoreType[next] || high <= low {
			continue
		}
		if seen, ok := steps[coreType]; ok && seen != high-low {
			rejected[coreType] = true
			continue
		}
		steps[coreType] = high - low
	}
	for coreType := range rejected {
		delete(steps, coreType)
	}
	return steps
}

var cpuDirRegexp = regexp.MustCompile(`^cpu(\d+)$`)

func parseCPUDirName(name string) (int, bool) {
	m := cpuDirRegexp.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func hybridCPUSet(sysRoot, pmuName string) map[int]bool {
	raw, err := os.ReadFile(filepath.Join(sysRoot, "bus", "event_source", "devices", pmuName, "cpus"))
	if err != nil {
		return nil
	}
	return parseCPUList(strings.TrimSpace(string(raw)))
}

// isolatedCPUSet reads the kernel's authoritative isolated CPU list. Linux
// also exposes nohz_full and rcu_nocbs lists, but neither is a declaration of
// CPU isolation: they can overlap with isolated CPUs without defining it.
// Their presence therefore must never classify a CPU as isolated.
func isolatedCPUSet(sysRoot string) (map[int]bool, bool) {
	raw, err := os.ReadFile(filepath.Join(sysRoot, "devices", "system", "cpu", "isolated"))
	if err != nil {
		return nil, false
	}
	return parseCPUList(strings.TrimSpace(string(raw))), true
}

// parseCPUList parses the kernel's cpu-list range syntax, e.g.
// "0-3,8,10-11" -> {0,1,2,3,8,10,11}. Used both for hybrid PMU device
// lists here and, in reader.go, for a CPU offline range if ever needed.
func parseCPUList(s string) map[int]bool {
	set := map[int]bool{}
	if s == "" {
		return set
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			loN, err1 := strconv.Atoi(lo)
			hiN, err2 := strconv.Atoi(hi)
			if err1 != nil || err2 != nil {
				continue
			}
			for i := loN; i <= hiN; i++ {
				set[i] = true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			set[n] = true
		}
	}
	return set
}

// OfflineCPUs returns every logical CPU currently marked offline, sorted.
func (t Topology) OfflineCPUs() []int {
	var offline []int
	for id, online := range t.Online {
		if !online {
			offline = append(offline, id)
		}
	}
	sort.Ints(offline)
	return offline
}

// ActiveCores returns the set of distinct physical core IDs that have at
// least one online logical CPU — the binomial test's null-hypothesis
// category count (Observe in tracker.go).
func (t Topology) ActiveCores() map[int]bool {
	cores := map[int]bool{}
	for id, coreID := range t.LogicalToCore {
		if t.Online[id] {
			cores[coreID] = true
		}
	}
	return cores
}
