package hwidentity

import (
	"context"
	"os"
	"path/filepath"
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
