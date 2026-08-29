package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
	"github.com/bitacora-dev/bitacora/internal/vpnhelper"
)

type recordingSink struct {
	gauges      map[string]float64
	inventories []schema.Inventory
}

func newRecordingSink() *recordingSink { return &recordingSink{gauges: map[string]float64{}} }

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	key := name
	if iface, ok := labels["interface"]; ok {
		key = name + "{" + iface + "}"
	}
	s.gauges[key] = value
}
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

func procNetDevFixture(eth0RxBytes, eth0TxBytes uint64) string {
	rx := strconv.FormatUint(eth0RxBytes, 10)
	tx := strconv.FormatUint(eth0TxBytes, 10)
	return "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo:  123456     100    0    0    0     0          0         0   123456     100    0    0    0     0       0          0\n" +
		"  eth0: " + rx + "  654321    0    0    0     0          0         0 " + tx + "  456789    0    0    0     0       0          0\n"
}

// writeVPNSpoolEntry writes a real spool entry (via spool.WriteAtomic,
// the same helper bitacora-vpn itself uses) so the collector reads
// exactly the on-disk shape production code produces.
func writeVPNSpoolEntry(t *testing.T, dir, wireguardDump, tailscaleJSON string) {
	t.Helper()
	result := vpnhelper.Result{WireguardDump: wireguardDump, TailscaleStatusJSON: tailscaleJSON}
	if err := spool.WriteAtomic(dir, "vpn", 1, result, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// backdateSpoolEntry rewrites a spool entry's "ts" field in place, to
// simulate one written long enough ago to count as stale.
func backdateSpoolEntry(t *testing.T, path string, ts time.Time) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var entry spool.Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry.TS = ts
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollector_FirstSampleNoRateYet(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevFixture(1000, 2000))

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    filepath.Join(dir, "spool"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.gauges) != 0 {
		t.Fatalf("expected no rate gauges on the first sample, got %+v", sink.gauges)
	}
	// loopback must never appear even as a raw reading.
	if _, ok := sink.gauges["bitacora_net_rx_bytes_per_second{lo}"]; ok {
		t.Fatal("expected loopback to be excluded")
	}
}

func TestCollector_SecondSampleComputesRate(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevFixture(1000, 2000))

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    filepath.Join(dir, "spool"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.Collect(context.Background(), newRecordingSink())

	// Simulate 1 second passing with 500 more rx bytes, 1000 more tx bytes.
	c.prevAt = time.Now().Add(-time.Second)
	writeFile(t, procNetDev, procNetDevFixture(1500, 3000))

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rx := sink.gauges["bitacora_net_rx_bytes_per_second{eth0}"]
	tx := sink.gauges["bitacora_net_tx_bytes_per_second{eth0}"]
	if rx < 400 || rx > 600 {
		t.Fatalf("expected rx rate around 500 B/s, got %v", rx)
	}
	if tx < 900 || tx > 1100 {
		t.Fatalf("expected tx rate around 1000 B/s, got %v", tx)
	}
}

func TestCollector_CounterResetSkipsThatCycle(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevFixture(5000, 5000))

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    filepath.Join(dir, "spool"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.Collect(context.Background(), newRecordingSink())

	// Interface counters went backwards (e.g. the NIC reset) — must not
	// report a nonsensical negative-turned-huge-positive rate.
	c.prevAt = time.Now().Add(-time.Second)
	writeFile(t, procNetDev, procNetDevFixture(100, 100))

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := sink.gauges["bitacora_net_rx_bytes_per_second{eth0}"]; ok {
		t.Fatalf("expected the reset cycle to be skipped, got %+v", sink.gauges)
	}
}

func TestCollector_NoVPNSpoolEntryYieldsEmptyInventory(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	writeFile(t, procNetDev, procNetDevFixture(1, 1))

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    filepath.Join(dir, "spool"), // never created
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 1 || sink.inventories[0].Kind != schema.InventoryVPNTunnel {
		t.Fatalf("expected a vpn_tunnel inventory (empty), got %+v", sink.inventories)
	}
	if len(sink.inventories[0].Items) != 0 {
		t.Fatalf("expected no items without a spool entry, got %+v", sink.inventories[0].Items)
	}
}

func TestCollector_ParsesWireguardAndTailscaleFromSpool(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	spoolDir := filepath.Join(dir, "spool")
	writeFile(t, procNetDev, procNetDevFixture(1, 1))

	now := time.Now().Unix()
	wgDump := "wg0\tprivkeyXXXX\tpubkeyXXXX\t51820\toff\n" +
		"wg0\tpeerpubkeyABCDEFGHIJKLMNOP\t(none)\t203.0.113.5:51820\t10.10.0.2/32\t" + strconv.FormatInt(now, 10) + "\t1024\t2048\t25\n"
	tsJSON := `{"BackendState":"Running","Self":{"HostName":"icloudserver","Online":true}}`

	writeVPNSpoolEntry(t, spoolDir, wgDump, tsJSON)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    spoolDir,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := sink.inventories[0].Items
	if len(items) != 2 {
		t.Fatalf("expected 1 wireguard peer + 1 tailscale summary, got %d: %+v", len(items), items)
	}

	var wgItem, tsItem *schema.InventoryItem
	for i := range items {
		switch items[i].Attrs["protocol"] {
		case "wireguard":
			wgItem = &items[i]
		case "tailscale":
			tsItem = &items[i]
		}
	}
	if wgItem == nil {
		t.Fatal("expected a wireguard item")
	}
	if wgItem.Name != "wg0" || wgItem.Attrs["endpoint"] != "203.0.113.5:51820" || wgItem.Attrs["active"] != "true" {
		t.Fatalf("unexpected wireguard item: %+v", wgItem)
	}

	if tsItem == nil {
		t.Fatal("expected a tailscale item")
	}
	if tsItem.Attrs["active"] != "true" || tsItem.Attrs["state"] != "Running" {
		t.Fatalf("unexpected tailscale item: %+v", tsItem)
	}
}

func TestCollector_StaleVPNSpoolEntryIsIgnored(t *testing.T) {
	dir := t.TempDir()
	procNetDev := filepath.Join(dir, "net_dev")
	spoolDir := filepath.Join(dir, "spool")
	writeFile(t, procNetDev, procNetDevFixture(1, 1))
	wgDump := "wg0\tpub\t(none)\t203.0.113.5:51820\t10.10.0.2/32\t" + strconv.FormatInt(time.Now().Unix(), 10) + "\t1024\t2048\t25\n"
	writeVPNSpoolEntry(t, spoolDir, wgDump, "")

	// Back-date the spool entry's own ts field well past staleAfter.
	entryPath := filepath.Join(spoolDir, "vpn.json")
	backdateSpoolEntry(t, entryPath, time.Now().Add(-2*staleAfter))

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev": procNetDev,
		"spool_dir":    spoolDir,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories[0].Items) != 0 {
		t.Fatalf("expected a stale spool entry to be ignored, got %+v", sink.inventories[0].Items)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, newRecordingSink()); err == nil {
		t.Fatal("expected cancellation error")
	}
}
