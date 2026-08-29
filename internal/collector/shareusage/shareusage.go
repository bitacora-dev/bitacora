// Package shareusage implements ADR-0016's "how much does this share
// actually occupy" measurement: a periodic, low-frequency directory walk
// (the equivalent of `du -sh`) over each configured share's path. This is
// deliberately its own collector, registered at a much longer interval
// than internal/collector/shares — walking a multi-terabyte media share
// can take minutes, which is completely wrong for the same 5-minute cycle
// share *definitions* use.
package shareusage

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/collector/shares"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultSambaConf   = "/etc/samba/smb.conf"
	defaultExportsFile = "/etc/exports"
)

// Collector emits an Inventory of kind share_usage (ADR-0016).
type Collector struct {
	sambaConf   string
	exportsFile string
	hostID      string
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "shareusage" }

// Requires implements collector.Collector. Same reasoning as
// internal/collector/shares.Requires: this needs "at least one of
// SMB/NFS", which the Registry's AND-only check can't express, so it
// self-gates on whatever shares.Paths actually finds.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.sambaConf = configuredPath(cfg, "samba_conf", defaultSambaConf)
	c.exportsFile = configuredPath(cfg, "exports_file", defaultExportsFile)
	if host != nil {
		c.hostID = host.ID
	}
	return nil
}

// Collect implements collector.Collector. Each share's size is measured
// independently; one share failing to walk (permission denied, path
// gone) doesn't lose the others' measurements.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	paths := shares.Paths(c.sambaConf, c.exportsFile)
	now := time.Now().UTC()

	items := make([]schema.InventoryItem, 0, len(paths))
	for id, path := range paths {
		usedBytes, err := dirSize(ctx, path)
		if err != nil {
			continue // this share's size just isn't reported this cycle
		}
		items = append(items, schema.InventoryItem{
			ID:   id,
			Name: id,
			Attrs: schema.Labels{
				"used_bytes":    strconv.FormatUint(usedBytes, 10),
				"calculated_at": now.Format(time.RFC3339),
			},
		})
	}

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryShareUsage,
		ReportedAt: now,
		Schema:     schema.CurrentSchemaVersion,
		Items:      items,
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

// dirSize sums every regular file's size under root, checking ctx
// periodically so a walk over a genuinely huge share can still be
// cancelled instead of running unbounded. A single unreadable file or
// subdirectory is skipped, not fatal to the whole walk — matching the
// project's usual "best effort, don't let one bad entry blind the rest"
// posture.
func dirSize(ctx context.Context, root string) (uint64, error) {
	// Fail fast if the root itself doesn't exist or isn't readable — the
	// walk callback below deliberately swallows errors on entries *within*
	// an opened root (one bad file shouldn't lose the rest), which would
	// otherwise also swallow "the whole share is gone" into a silent 0.
	if _, err := os.Lstat(root); err != nil {
		return 0, err
	}

	var total uint64
	var checked int

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		checked++
		if checked%100 == 0 {
			if cErr := ctx.Err(); cErr != nil {
				return cErr
			}
		}
		if err != nil {
			return nil // unreadable entry — skip it, keep walking
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
