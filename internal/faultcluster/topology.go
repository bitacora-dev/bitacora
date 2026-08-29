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

// Topology maps logical CPUs to their physical core, online state, and —
// best-effort — hybrid core type.
type Topology struct {
	LogicalToCore map[int]int
	CoreType      map[int]CoreType
	Online        map[int]bool
}

// ReadTopology reads /sys/devices/system/cpu (physical core mapping and
// online state) and, when present, the hybrid-CPU PMU device lists at
// /sys/bus/event_source/devices/{cpu_core,cpu_atom}/cpus (P-core/E-core
// classification). The latter only exists on Intel hybrid CPUs running a
// kernel new enough to expose it — its absence isn't an error, every
// logical CPU is simply CoreTypeUnknown.
func ReadTopology(sysRoot string) (Topology, error) {
	cpuRoot := filepath.Join(sysRoot, "devices", "system", "cpu")
	entries, err := os.ReadDir(cpuRoot)
	if err != nil {
		return Topology{}, err
	}

	topo := Topology{
		LogicalToCore: map[int]int{},
		CoreType:      map[int]CoreType{},
		Online:        map[int]bool{},
	}

	for _, e := range entries {
		id, ok := parseCPUDirName(e.Name())
		if !ok {
			continue
		}

		coreID := id // no topology info (no SMT, or the file's absent) means the CPU is its own core
		if raw, err := os.ReadFile(filepath.Join(cpuRoot, e.Name(), "topology", "core_id")); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				coreID = n
			}
		}
		topo.LogicalToCore[id] = coreID
		topo.CoreType[id] = CoreTypeUnknown

		// cpu0 (and any CPU that can't be offlined) has no "online" file
		// at all — its absence means "always online", not "unknown".
		online := true
		if raw, err := os.ReadFile(filepath.Join(cpuRoot, e.Name(), "online")); err == nil {
			online = strings.TrimSpace(string(raw)) == "1"
		}
		topo.Online[id] = online
	}

	for id := range hybridCPUSet(sysRoot, "cpu_core") {
		if _, ok := topo.LogicalToCore[id]; ok {
			topo.CoreType[id] = CoreTypeP
		}
	}
	for id := range hybridCPUSet(sysRoot, "cpu_atom") {
		if _, ok := topo.LogicalToCore[id]; ok {
			topo.CoreType[id] = CoreTypeE
		}
	}

	return topo, nil
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
