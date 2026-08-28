// Package memory implements the memory usage collector (ADR-0007): total,
// available and swap, read from /proc/meminfo via procfs. Requires no host
// capability — /proc/meminfo is always readable without privilege.
package memory

import (
	"context"
	"fmt"

	"github.com/prometheus/procfs"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// Collector emits memory usage in bytes, plus a used-ratio gauge.
type Collector struct {
	fs procfs.FS
}

// New returns a collector that reads the real /proc.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "memory" }

// Requires implements collector.Collector. /proc/meminfo needs no capability.
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

	mi, err := c.fs.Meminfo()
	if err != nil {
		return fmt.Errorf("reading /proc/meminfo: %w", err)
	}

	if mi.MemTotal != nil {
		sink.Gauge("bitacora_memory_total_bytes", kbToBytes(*mi.MemTotal), nil)
	}
	if mi.MemAvailable != nil {
		sink.Gauge("bitacora_memory_available_bytes", kbToBytes(*mi.MemAvailable), nil)
	}
	if mi.MemTotal != nil && mi.MemAvailable != nil && *mi.MemTotal > 0 {
		used := float64(*mi.MemTotal - *mi.MemAvailable)
		sink.Gauge("bitacora_memory_used_ratio", used/float64(*mi.MemTotal), nil)
	}
	if mi.SwapTotal != nil {
		sink.Gauge("bitacora_memory_swap_total_bytes", kbToBytes(*mi.SwapTotal), nil)
	}
	if mi.SwapFree != nil {
		sink.Gauge("bitacora_memory_swap_free_bytes", kbToBytes(*mi.SwapFree), nil)
	}

	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func kbToBytes(kb uint64) float64 { return float64(kb) * 1024 }
