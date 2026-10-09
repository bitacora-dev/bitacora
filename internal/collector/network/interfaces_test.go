package network

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// procNetDevWith builds a /proc/net/dev fixture from interface name to
// {rx, tx}, in the real fixed-column format.
func procNetDevWith(counters map[string][2]uint64) string {
	b := strings.Builder{}
	b.WriteString("Inter-|   Receive                                                |  Transmit\n")
	b.WriteString(" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n")
	b.WriteString("    lo:  123456     100    0    0    0     0          0         0   123456     100    0    0    0     0       0          0\n")
	for iface, v := range counters {
		b.WriteString("  " + iface + ": " + strconv.FormatUint(v[0], 10) +
			"  654321    0    0    0     0          0         0 " + strconv.FormatUint(v[1], 10) +
			"  456789    0    0    0     0       0          0\n")
	}
	return b.String()
}

// fakeSysClassNet builds the sysfs shape Linux really exposes: one symlink
// per interface into the device tree, with software netdevs pointing under
// devices/virtual/net.
func fakeSysClassNet(t *testing.T, root string, virtual map[string]bool) string {
	t.Helper()
	sysClassNet := filepath.Join(root, "sys", "class", "net")
	if err := os.MkdirAll(sysClassNet, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for iface, isVirtual := range virtual {
		target := "../../devices/pci0000:00/0000:00:1f.6/net/" + iface
		if isVirtual {
			target = "../../devices/virtual/net/" + iface
		}
		if err := os.Symlink(target, filepath.Join(sysClassNet, iface)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return sysClassNet
}

func collectInterfaces(t *testing.T, procNetDev, sysClassNet, spoolDir string) map[string]float64 {
	t.Helper()
	c := New()
	cfg := collector.Config{"proc_net_dev": procNetDev, "spool_dir": spoolDir}
	if sysClassNet != "" {
		cfg["sys_class_net"] = sysClassNet
	}
	if err := c.Init(context.Background(), cfg, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return sink.counters
}

// TestSelectHostInterfaces_CountsOnlyDeviceBackedOnes is the semantic half
// of the false-number bug: summing every interface counts a container's
// byte on its veth, again on the bridge and again on the NIC it leaves
// through. Only device-backed interfaces are bytes actually entering or
// leaving the machine.
func TestSelectHostInterfaces_CountsOnlyDeviceBackedOnes(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{
		"eno1":            {19714, 68648},
		"eno2":            {395, 138},
		"tailscale0":      {1900, 57952},
		"docker0":         {10, 20},
		"docker_gwbridge": {58047, 2570},
		"br-9f2a1c":       {5, 5},
		"veth4f21a9":      {700, 800},
	}))
	sysClassNet := fakeSysClassNet(t, dir, map[string]bool{
		"eno1": false, "eno2": false,
		"tailscale0": true, "docker0": true, "docker_gwbridge": true,
		"br-9f2a1c": true, "veth4f21a9": true,
	})

	counters := collectInterfaces(t, procNetDev, sysClassNet, filepath.Join(dir, "spool"))

	for _, iface := range []string{"eno1", "eno2"} {
		if _, ok := counters["bitacora_net_rx_bytes_total{"+iface+"}"]; !ok {
			t.Fatalf("expected the device-backed interface %s to be counted, got %+v", iface, counters)
		}
	}
	for _, iface := range []string{"tailscale0", "docker0", "docker_gwbridge", "br-9f2a1c", "veth4f21a9"} {
		if _, ok := counters["bitacora_net_rx_bytes_total{"+iface+"}"]; ok {
			t.Fatalf("expected the virtual interface %s to be excluded, got %+v", iface, counters)
		}
	}
	if len(counters) != 4 { // rx+tx for eno1 and eno2
		t.Fatalf("expected 4 counters (rx+tx for two interfaces), got %d: %+v", len(counters), counters)
	}
}

// TestSelectHostInterfaces_FallsBackToNamesWhenEverythingIsVirtual covers
// the agent running inside a container: its own eth0 is the far end of a
// veth pair, so sysfs calls it virtual. Dropping every interface there
// would report nothing at all, which is the same lie as reporting 0 B/s.
func TestSelectHostInterfaces_FallsBackToNamesWhenEverythingIsVirtual(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{
		"eth0":       {1000, 2000},
		"veth4f21a9": {700, 800},
		"docker0":    {10, 20},
	}))
	sysClassNet := fakeSysClassNet(t, dir, map[string]bool{
		"eth0": true, "veth4f21a9": true, "docker0": true,
	})

	counters := collectInterfaces(t, procNetDev, sysClassNet, filepath.Join(dir, "spool"))

	if got := counters["bitacora_net_rx_bytes_total{eth0}"]; got != 1000 {
		t.Fatalf("expected eth0 to survive the name fallback with 1000, got %v (%+v)", got, counters)
	}
	if _, ok := counters["bitacora_net_rx_bytes_total{veth4f21a9}"]; ok {
		t.Fatalf("expected the veth to be excluded by name, got %+v", counters)
	}
	if _, ok := counters["bitacora_net_rx_bytes_total{docker0}"]; ok {
		t.Fatalf("expected docker0 to be excluded by name, got %+v", counters)
	}
}

// TestSelectHostInterfaces_NameFallbackWithoutSysfs asserts the same
// fallback when sysfs isn't readable at all, rather than reporting every
// interface as if nothing had been decided.
func TestSelectHostInterfaces_NameFallbackWithoutSysfs(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{
		"eno1":       {1000, 2000},
		"wlan0":      {300, 400},
		"br0":        {1, 1},
		"bond0":      {9, 9},
		"wg0":        {5, 5},
		"veth4f21a9": {700, 800},
	}))

	counters := collectInterfaces(t, procNetDev, filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "spool"))

	for _, iface := range []string{"eno1", "wlan0"} {
		if _, ok := counters["bitacora_net_rx_bytes_total{"+iface+"}"]; !ok {
			t.Fatalf("expected %s to be counted, got %+v", iface, counters)
		}
	}
	for _, iface := range []string{"br0", "bond0", "wg0", "veth4f21a9"} {
		if _, ok := counters["bitacora_net_rx_bytes_total{"+iface+"}"]; ok {
			t.Fatalf("expected %s to be excluded by name, got %+v", iface, counters)
		}
	}
}

// TestSelectHostInterfaces_ContainerChurnDoesNotChangeTheSeries asserts
// that starting and stopping containers — which creates and destroys veth
// interfaces between cycles — leaves the emitted series identical. That is
// what keeps an appearing or disappearing interface from showing up as a
// spike or a gap in the traffic panel.
func TestSelectHostInterfaces_ContainerChurnDoesNotChangeTheSeries(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	sysClassNet := fakeSysClassNet(t, dir, map[string]bool{"eno1": false})

	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{"eno1": {1000, 2000}}))
	before := collectInterfaces(t, procNetDev, sysClassNet, filepath.Join(dir, "spool"))

	// A container starts: a new veth appears in /proc/net/dev and in sysfs.
	if err := os.Symlink("../../devices/virtual/net/vethNEW", filepath.Join(sysClassNet, "vethNEW")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{
		"eno1": {2000, 4000}, "vethNEW": {999999, 999999},
	}))
	during := collectInterfaces(t, procNetDev, sysClassNet, filepath.Join(dir, "spool"))

	// It stops again.
	if err := os.Remove(filepath.Join(sysClassNet, "vethNEW")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{"eno1": {3000, 6000}}))
	after := collectInterfaces(t, procNetDev, sysClassNet, filepath.Join(dir, "spool"))

	for label, counters := range map[string]map[string]float64{"before": before, "during": during, "after": after} {
		if len(counters) != 2 {
			t.Fatalf("%s: expected only eno1's rx+tx, got %+v", label, counters)
		}
	}
	if during["bitacora_net_rx_bytes_total{eno1}"] != 2000 || after["bitacora_net_rx_bytes_total{eno1}"] != 3000 {
		t.Fatalf("expected eno1's own counter to keep advancing, got during=%+v after=%+v", during, after)
	}
}

// TestCollector_VPNInventoryKeepsItsOwnCadence asserts that shortening the
// traffic cadence to 10s did not triple how often the tunnel Inventory is
// written: it stays on vpnReportInterval.
func TestCollector_VPNInventoryKeepsItsOwnCadence(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevWith(map[string][2]uint64{"eth0": {1, 1}}))

	now := time.Unix(1_700_000_000, 0)
	c := New()
	c.now = func() time.Time { return now }
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    filepath.Join(dir, "spool"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := newRecordingSink()
	// Three 10s traffic cycles inside one vpnReportInterval.
	for i := 0; i < 3; i++ {
		if err := c.Collect(context.Background(), sink); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		now = now.Add(10 * time.Second)
	}
	if len(sink.inventories) != 1 {
		t.Fatalf("expected one vpn inventory within %s, got %d", vpnReportInterval, len(sink.inventories))
	}

	// The fourth cycle crosses it.
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 2 {
		t.Fatalf("expected a second vpn inventory once %s elapsed, got %d", vpnReportInterval, len(sink.inventories))
	}
}
