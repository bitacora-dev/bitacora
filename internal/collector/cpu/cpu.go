// Package cpu implements the CPU usage collector (ADR-0007): per-core and
// aggregate usage ratio, read from /proc/stat via procfs. Requires no host
// capability — /proc/stat is always readable without privilege (ADR-0004,
// ADR-0005).
package cpu

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/prometheus/procfs"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// Collector emits CPU usage as a 0-1 ratio, total and per-core.
type Collector struct {
	fs procfs.FS

	prevAt     time.Time
	prevTotal  procfs.CPUStat
	prevPerCPU map[int64]procfs.CPUStat
}

// New returns a collector that reads the real /proc.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "cpu" }

// Requires implements collector.Collector. /proc/stat needs no capability.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector. cfg["procfs_path"] overrides the
// procfs mount point — used by tests to point at testdata instead of the
// real /proc.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	mountPoint := procfs.DefaultMountPoint
	if v, ok := cfg["procfs_path"].(string); ok && v != "" {
		mountPoint = v
	}

	fs, err := procfs.NewFS(mountPoint)
	if err != nil {
		return fmt.Errorf("opening procfs at %s: %w", mountPoint, err)
	}
	c.fs = fs
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	stat, err := c.fs.Stat()
	if err != nil {
		return fmt.Errorf("reading /proc/stat: %w", err)
	}

	now := time.Now()
	if !c.prevAt.IsZero() {
		if ratio, ok := cpuUsageRatio(c.prevTotal, stat.CPUTotal); ok {
			sink.Gauge("bitacora_cpu_usage_ratio", ratio, collector.Labels{"cpu": "total"})
		}

		ids := make([]int64, 0, len(stat.CPU))
		for id := range stat.CPU {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		for _, id := range ids {
			prev, ok := c.prevPerCPU[id]
			if !ok {
				continue // a CPU that came online between samples — nothing to diff yet
			}
			if ratio, ok := cpuUsageRatio(prev, stat.CPU[id]); ok {
				sink.Gauge("bitacora_cpu_usage_ratio", ratio, collector.Labels{"cpu": fmt.Sprintf("%d", id)})
			}
		}
	}

	c.prevTotal = stat.CPUTotal
	c.prevPerCPU = stat.CPU
	c.prevAt = now
	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

// cpuUsageRatio computes the fraction of time busy (not idle, not iowait)
// between two cumulative /proc/stat samples. ok is false on the very first
// sample (no prior baseline) or if the counters didn't advance.
func cpuUsageRatio(prev, cur procfs.CPUStat) (ratio float64, ok bool) {
	prevTotal := cpuStatTotal(prev)
	curTotal := cpuStatTotal(cur)
	totalDelta := curTotal - prevTotal
	if totalDelta <= 0 {
		return 0, false
	}

	idleDelta := (cur.Idle + cur.Iowait) - (prev.Idle + prev.Iowait)
	busyDelta := totalDelta - idleDelta

	r := busyDelta / totalDelta
	switch {
	case r < 0:
		r = 0
	case r > 1:
		r = 1
	}
	return r, true
}

// cpuStatTotal sums the counters that make up "all CPU time". Guest and
// GuestNice are excluded: on Linux they're already included in User and
// Nice respectively, and double-counting them would push the ratio over 1.
func cpuStatTotal(s procfs.CPUStat) float64 {
	return s.User + s.Nice + s.System + s.Idle + s.Iowait + s.IRQ + s.SoftIRQ + s.Steal
}
