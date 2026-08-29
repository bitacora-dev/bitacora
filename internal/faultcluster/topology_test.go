package faultcluster

import (
	"os"
	"path/filepath"
	"strconv"
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
