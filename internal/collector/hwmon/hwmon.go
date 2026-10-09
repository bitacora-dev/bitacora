// Package hwmon emits bounded CPU temperature metrics from Linux hwmon.
package hwmon

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
	sharedhwmon "github.com/bitacora-dev/bitacora/internal/hwmon"
)

const defaultSysRoot = "/sys"

// maxCPUSensors bounds this collector to 128 active series per host, leaving
// ample room inside ADR-0006's 2,000-series host budget.
const maxCPUSensors = 128

var cpuChips = map[string]bool{
	"coretemp": true,
	"k10temp":  true,
	"zenpower": true,
}

// Collector reports only CPU-package and CPU-core sensors. Disk, chipset,
// VRM, and power-supply hwmon devices are intentionally excluded: one
// bounded CPU sensor family is useful to the CPU panel without consuming the
// unguarded per-host series budget with every device's auxiliary sensors.
type Collector struct{ sysRoot string }

// New returns a collector using the host sysfs mount.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "hwmon" }

// Requires implements collector.Collector.
func (c *Collector) Requires() []collector.Capability {
	return []collector.Capability{capabilities.HwHwmon}
}

// Init implements collector.Collector.
func (c *Collector) Init(_ context.Context, cfg collector.Config, _ *collector.HostInfo) error {
	c.sysRoot = defaultSysRoot
	if value, ok := cfg["sys_root"].(string); ok && value != "" {
		c.sysRoot = value
	}
	return nil
}

// Collect implements collector.Collector. An absent or unreadable sensor is
// omitted rather than represented as a fabricated zero-degree measurement.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	temperatures, err := sharedhwmon.ReadTemperatures(filepath.Join(c.sysRoot, "class", "hwmon"))
	if err != nil {
		return nil
	}
	cpuTemperatures := make([]sharedhwmon.Temperature, 0, len(temperatures))
	for _, temperature := range temperatures {
		if !cpuChips[temperature.Chip] {
			continue
		}
		cpuTemperatures = append(cpuTemperatures, temperature)
	}
	sort.Slice(cpuTemperatures, func(i, j int) bool {
		left, right := metricSensorName(cpuTemperatures[i]), metricSensorName(cpuTemperatures[j])
		if cpuTemperatures[i].Chip == cpuTemperatures[j].Chip {
			return left < right
		}
		return cpuTemperatures[i].Chip < cpuTemperatures[j].Chip
	})
	for index, temperature := range cpuTemperatures {
		if index >= maxCPUSensors {
			break
		}
		sink.Gauge("bitacora_cpu_temperature_celsius", float64(temperature.MilliC)/1000, collector.Labels{
			"chip":   temperature.Chip,
			"sensor": metricSensorName(temperature),
		})
	}
	return nil
}

func metricSensorName(temperature sharedhwmon.Temperature) string {
	sensor := temperature.Label
	if sensor == "" {
		sensor = temperature.Input
	}
	return strings.ToLower(strings.Join(strings.Fields(sensor), "_"))
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }
