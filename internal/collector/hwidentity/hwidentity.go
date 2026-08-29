// Package hwidentity implements two ADR-0016 Inventory kinds about the
// physical machine itself: hardware_identity (motherboard, BIOS,
// processor, power draw) and cpu_topology (which logical CPUs exist,
// their physical core, online state, and hybrid P-core/E-core type).
// Both are read from whatever DMI/sysfs data is actually present — a VPS
// reports its hypervisor's virtual DMI table (e.g. "QEMU", "Google
// Compute Engine"), which is itself useful information, so this doesn't
// try to detect and suppress virtualized hosts.
package hwidentity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/faultcluster"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultSysRoot  = "/sys"
	defaultProcRoot = "/proc"
)

// Collector emits hardware_identity and cpu_topology Inventory snapshots.
type Collector struct {
	sysRoot  string
	procRoot string
	hostID   string

	prevEnergyUJ uint64
	prevAt       time.Time
	haveRAPL     bool
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "hwidentity" }

// Requires implements collector.Collector. Every field this collector
// reads degrades independently and gracefully (missing DMI files, no
// RAPL, no CPU topology) — no single capability gates the whole thing.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.sysRoot = configuredPath(cfg, "sys_root", defaultSysRoot)
	c.procRoot = configuredPath(cfg, "proc_root", defaultProcRoot)
	if host != nil {
		c.hostID = host.ID
	}
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	now := time.Now()

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryHardwareIdentity,
		ReportedAt: now.UTC(),
		Schema:     schema.CurrentSchemaVersion,
		Items:      c.hardwareIdentityItems(now),
	})

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryCPUTopology,
		ReportedAt: now.UTC(),
		Schema:     schema.CurrentSchemaVersion,
		Items:      cpuTopologyItems(c.sysRoot),
	})

	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func configuredPath(cfg collector.Config, key, fallback string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func (c *Collector) hardwareIdentityItems(now time.Time) []schema.InventoryItem {
	attrs := schema.Labels{}

	dmi := filepath.Join(c.sysRoot, "class", "dmi", "id")
	setIfPresent(attrs, "board_vendor", readTrimmed(filepath.Join(dmi, "board_vendor")))
	setIfPresent(attrs, "board_name", readTrimmed(filepath.Join(dmi, "board_name")))
	setIfPresent(attrs, "board_version", readTrimmed(filepath.Join(dmi, "board_version")))
	setIfPresent(attrs, "bios_vendor", readTrimmed(filepath.Join(dmi, "bios_vendor")))
	setIfPresent(attrs, "bios_version", readTrimmed(filepath.Join(dmi, "bios_version")))
	setIfPresent(attrs, "bios_date", readTrimmed(filepath.Join(dmi, "bios_date")))

	if model, ok := readCPUModel(filepath.Join(c.procRoot, "cpuinfo")); ok {
		attrs["cpu_model"] = model
	}

	if watts, ok := c.readRAPLWatts(now); ok {
		attrs["cpu_power_watts"] = strconv.FormatFloat(watts, 'f', 2, 64)
	}

	if len(attrs) == 0 {
		return nil
	}
	return []schema.InventoryItem{{ID: "system", Name: "system", Attrs: attrs}}
}

func setIfPresent(attrs schema.Labels, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// readCPUModel returns /proc/cpuinfo's first "model name" line.
func readCPUModel(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "model name" {
			continue
		}
		return strings.TrimSpace(value), true
	}
	return "", false
}

// readRAPLWatts computes average package power draw from Intel RAPL's
// cumulative microjoule energy counter (/sys/class/powercap/intel-rapl:0/
// energy_uj), which needs two readings a known time apart — the first
// call only seeds the baseline, same delta pattern as the CPU/network
// collectors. RAPL access can be root-only on kernels patched for
// CVE-2020-8694; an unreadable counter degrades to "no power reading",
// not an error.
func (c *Collector) readRAPLWatts(now time.Time) (float64, bool) {
	path := filepath.Join(c.sysRoot, "class", "powercap", "intel-rapl:0", "energy_uj")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	energy, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false
	}

	defer func() {
		c.prevEnergyUJ = energy
		c.prevAt = now
		c.haveRAPL = true
	}()

	if !c.haveRAPL || energy < c.prevEnergyUJ {
		return 0, false // first sample, or the counter wrapped/reset
	}
	elapsed := now.Sub(c.prevAt).Seconds()
	if elapsed <= 0 {
		return 0, false
	}
	deltaJoules := float64(energy-c.prevEnergyUJ) / 1_000_000
	return deltaJoules / elapsed, true
}

// cpuTopologyItems wraps internal/faultcluster.ReadTopology — that
// package already computes exactly this mapping for ADR-0011's
// correlation work, so it's reused here rather than reimplemented.
func cpuTopologyItems(sysRoot string) []schema.InventoryItem {
	topo, err := faultcluster.ReadTopology(sysRoot)
	if err != nil {
		return nil
	}

	items := make([]schema.InventoryItem, 0, len(topo.LogicalToCore))
	for cpu, coreID := range topo.LogicalToCore {
		attrs := schema.Labels{
			"core_id":   strconv.Itoa(coreID),
			"online":    strconv.FormatBool(topo.Online[cpu]),
			"core_type": string(topo.CoreType[cpu]),
		}
		items = append(items, schema.InventoryItem{
			ID:    fmt.Sprintf("cpu%d", cpu),
			Name:  fmt.Sprintf("cpu%d", cpu),
			Attrs: attrs,
		})
	}
	return items
}
