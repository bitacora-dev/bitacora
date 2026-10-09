package hwidentity

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type recordingSink struct {
	inventories []schema.Inventory
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}
func (s *recordingSink) Inventory(inv collector.Inventory) {
	s.inventories = append(s.inventories, inv)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

const cpuinfoFixture = `processor	: 0
vendor_id	: GenuineIntel
model name	: 13th Gen Intel(R) Core(TM) i9-13900K
cpu MHz		: 3000.000

processor	: 1
vendor_id	: GenuineIntel
model name	: 13th Gen Intel(R) Core(TM) i9-13900K
cpu MHz		: 3000.000
`

func setupFixtureRoot(t *testing.T) (sysRoot, procRoot string) {
	t.Helper()
	dir := t.TempDir()
	sysRoot = filepath.Join(dir, "sys")
	procRoot = filepath.Join(dir, "proc")

	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "board_vendor"), "ASUSTeK COMPUTER INC.\n")
	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "board_name"), "PRIME Z790-P WIFI\n")
	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "board_version"), "Rev 1.xx\n")
	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "bios_vendor"), "American Megatrends Inc.\n")
	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "bios_version"), "0806\n")
	writeFile(t, filepath.Join(sysRoot, "class", "dmi", "id", "bios_date"), "11/22/2022\n")
	writeFile(t, filepath.Join(procRoot, "cpuinfo"), cpuinfoFixture)

	// Minimal topology fixture: 2 CPUs, both core 0's threads (matches
	// internal/faultcluster's own test fixtures).
	writeFile(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu0", "topology", "core_id"), "0\n")
	writeFile(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu1", "topology", "core_id"), "0\n")
	writeFile(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu1", "online"), "1\n")

	return sysRoot, procRoot
}

func TestCollector_ReadsHardwareIdentity(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hwInv *schema.Inventory
	for i := range sink.inventories {
		if sink.inventories[i].Kind == schema.InventoryHardwareIdentity {
			hwInv = &sink.inventories[i]
		}
	}
	if hwInv == nil {
		t.Fatal("expected a hardware_identity inventory")
	}
	if len(hwInv.Items) != 1 {
		t.Fatalf("expected 1 system item, got %d", len(hwInv.Items))
	}
	attrs := hwInv.Items[0].Attrs
	if attrs["board_vendor"] != "ASUSTeK COMPUTER INC." || attrs["board_name"] != "PRIME Z790-P WIFI" {
		t.Fatalf("unexpected board attrs: %+v", attrs)
	}
	if attrs["bios_version"] != "0806" || attrs["bios_date"] != "11/22/2022" {
		t.Fatalf("unexpected bios attrs: %+v", attrs)
	}
	if !strings.Contains(attrs["cpu_model"], "i9-13900K") {
		t.Fatalf("expected cpu_model to contain the model string, got %q", attrs["cpu_model"])
	}
	// No RAPL fixture present on the first sample — never set.
	if _, ok := attrs["cpu_power_watts"]; ok {
		t.Fatalf("expected no power reading without a RAPL fixture, got %q", attrs["cpu_power_watts"])
	}
}

func TestCollector_ReadsCPUTopology(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)
	writeFile(t, filepath.Join(sysRoot, "devices", "system", "cpu", "isolated"), "1\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var topoInv *schema.Inventory
	for i := range sink.inventories {
		if sink.inventories[i].Kind == schema.InventoryCPUTopology {
			topoInv = &sink.inventories[i]
		}
	}
	if topoInv == nil {
		t.Fatal("expected a cpu_topology inventory")
	}
	if len(topoInv.Items) != 2 {
		t.Fatalf("expected 2 CPUs, got %d: %+v", len(topoInv.Items), topoInv.Items)
	}

	byID := map[string]schema.InventoryItem{}
	for _, item := range topoInv.Items {
		byID[item.ID] = item
	}
	if byID["cpu0"].Attrs["core_id"] != "0" || byID["cpu1"].Attrs["core_id"] != "0" {
		t.Fatalf("expected both CPUs on core 0, got %+v", byID)
	}
	// cpu0 has no "online" file (always on); cpu1 explicitly online=1.
	if byID["cpu0"].Attrs["online"] != "true" || byID["cpu1"].Attrs["online"] != "true" {
		t.Fatalf("expected both online, got %+v", byID)
	}
	if byID["cpu0"].Attrs["isolated"] != "false" || byID["cpu1"].Attrs["isolated"] != "true" {
		t.Fatalf("expected only cpu1 isolated, got %+v", byID)
	}
}

func TestCollector_RAPLComputesWattsOnSecondSample(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)
	raplPath := filepath.Join(sysRoot, "class", "powercap", "intel-rapl:0", "energy_uj")
	writeFile(t, raplPath, "1000000\n") // 1 joule

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.Collect(context.Background(), &recordingSink{})

	// Simulate 1 second passing with 30 more joules consumed -> ~30W.
	c.prevAt = time.Now().Add(-time.Second)
	writeFile(t, raplPath, "31000000\n")

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hwInv *schema.Inventory
	for i := range sink.inventories {
		if sink.inventories[i].Kind == schema.InventoryHardwareIdentity {
			hwInv = &sink.inventories[i]
		}
	}
	watts := hwInv.Items[0].Attrs["cpu_power_watts"]
	if watts == "" {
		t.Fatal("expected a power reading on the second sample")
	}
	// Allow generous slack for test timing jitter.
	if !strings.HasPrefix(watts, "2") && !strings.HasPrefix(watts, "3") {
		t.Fatalf("expected roughly 30W, got %q", watts)
	}
}

func TestCollector_MissingDataYieldsNoItemsNotError(t *testing.T) {
	dir := t.TempDir()
	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  filepath.Join(dir, "no-sys"),
		"proc_root": filepath.Join(dir, "no-proc"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 2 {
		t.Fatalf("expected both inventory kinds still emitted (empty), got %d", len(sink.inventories))
	}
	for _, inv := range sink.inventories {
		if len(inv.Items) != 0 {
			t.Fatalf("expected no items without any source data, got %+v", inv)
		}
	}
}

func TestCollector_MissingIsolatedListOmitsIsolationAttribute(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, inv := range sink.inventories {
		if inv.Kind != schema.InventoryCPUTopology {
			continue
		}
		for _, item := range inv.Items {
			if _, ok := item.Attrs["isolated"]; ok {
				t.Fatalf("expected no isolation datum without isolated sysfs file, got %+v", item.Attrs)
			}
		}
		return
	}
	t.Fatal("expected a cpu_topology inventory")
}

func TestCollector_EmptyIsolatedListMarksCPUsNotIsolated(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)
	writeFile(t, filepath.Join(sysRoot, "devices", "system", "cpu", "isolated"), "\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, inv := range sink.inventories {
		if inv.Kind != schema.InventoryCPUTopology {
			continue
		}
		for _, item := range inv.Items {
			if item.Attrs["isolated"] != "false" {
				t.Fatalf("expected %s not isolated with an empty isolated CPU list, got %+v", item.ID, item.Attrs)
			}
		}
		return
	}
	t.Fatal("expected a cpu_topology inventory")
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestCollect_CPUTopologyReportsOfflineCoreWithItsRealCore(t *testing.T) {
	sysRoot, procRoot := setupFixtureRoot(t)
	cpuRoot := filepath.Join(sysRoot, "devices", "system", "cpu")
	// Grow the fixture to the eight logical CPUs of four SMT cores numbered
	// 0, 4, 8 and 12 — Intel's own spacing — then take core 4's two threads
	// down the way icloudserver does: cpu2/cpu3 keep their "online" file and
	// lose topology/ entirely. cpu2's number is no core's core_id here, but
	// the reconstruction still has to name core 4 rather than invent one.
	writeFile(t, filepath.Join(cpuRoot, "cpu2", "online"), "0\n")
	writeFile(t, filepath.Join(cpuRoot, "cpu3", "online"), "0\n")
	for cpu, core := range map[int]int{4: 8, 5: 8, 6: 12, 7: 12} {
		dir := filepath.Join(cpuRoot, "cpu"+strconv.Itoa(cpu))
		writeFile(t, filepath.Join(dir, "topology", "core_id"), strconv.Itoa(core)+"\n")
		writeFile(t, filepath.Join(dir, "online"), "1\n")
	}
	// cpu10 is offline past the last readable CPU: nothing brackets it, so
	// its core is unknown. cpu11 is online but publishes no topology (as some
	// VMs do): its core is not reconstructed either, and it is not offline.
	for cpu := 8; cpu <= 9; cpu++ {
		dir := filepath.Join(cpuRoot, "cpu"+strconv.Itoa(cpu))
		writeFile(t, filepath.Join(dir, "topology", "core_id"), "16\n")
		writeFile(t, filepath.Join(dir, "online"), "1\n")
	}
	writeFile(t, filepath.Join(cpuRoot, "cpu10", "online"), "0\n")
	writeFile(t, filepath.Join(cpuRoot, "cpu11", "online"), "1\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"sys_root":  sysRoot,
		"proc_root": procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byID := map[string]schema.InventoryItem{}
	found := false
	for _, inv := range sink.inventories {
		if inv.Kind != schema.InventoryCPUTopology {
			continue
		}
		found = true
		for _, item := range inv.Items {
			byID[item.ID] = item
		}
	}
	if !found {
		t.Fatal("expected a cpu_topology inventory")
	}

	for _, cpu := range []string{"cpu2", "cpu3"} {
		if got := byID[cpu].Attrs["core_id"]; got != "4" {
			t.Errorf("expected offline %s on core 4, got %q", cpu, got)
		}
		if byID[cpu].Attrs["online"] != "false" {
			t.Errorf("expected %s offline, got %q", cpu, byID[cpu].Attrs["online"])
		}
		if byID[cpu].Attrs["core_id_inferred"] != "true" {
			t.Errorf("expected %s to declare its core reconstructed, got %q", cpu, byID[cpu].Attrs["core_id_inferred"])
		}
	}
	// Neither placeholder may be published as if it were a core number, and
	// neither CPU was reconstructed.
	for _, cpu := range []string{"cpu10", "cpu11"} {
		if got, ok := byID[cpu].Attrs["core_id"]; ok {
			t.Errorf("expected no core_id for %s, whose core is unknown, got %q", cpu, got)
		}
		if got, ok := byID[cpu].Attrs["core_id_inferred"]; ok {
			t.Errorf("expected no inference flag on %s, got %q", cpu, got)
		}
	}
	if byID["cpu11"].Attrs["online"] != "true" {
		t.Errorf("expected cpu11 online, got %q", byID["cpu11"].Attrs["online"])
	}
	// The running cores must read exactly as before, inference flag included.
	for cpu, core := range map[string]string{"cpu0": "0", "cpu1": "0", "cpu4": "8", "cpu5": "8", "cpu6": "12", "cpu7": "12"} {
		if got := byID[cpu].Attrs["core_id"]; got != core {
			t.Errorf("expected %s on core %s, got %q", cpu, core, got)
		}
		if _, ok := byID[cpu].Attrs["core_id_inferred"]; ok {
			t.Errorf("expected no inference flag on readable %s, got %q", cpu, byID[cpu].Attrs["core_id_inferred"])
		}
	}
}
