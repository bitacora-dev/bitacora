// Package shares implements the SMB/NFS share inventory collector
// (ADR-0015). It reads each service's static configuration — never
// queries the running daemon for live connection counts (ADR-0011's
// "Alternativas consideradas": that would need `exec`, and answers a
// different question than "what shares exist").
package shares

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultSambaConf   = "/etc/samba/smb.conf"
	defaultExportsFile = "/etc/exports"
)

// sambaMetaSections are share-like sections Samba defines for its own
// purposes, not real shares an operator configured.
var sambaMetaSections = map[string]bool{
	"global":   true,
	"homes":    true,
	"printers": true,
	"print$":   true,
}

// Collector emits an Inventory of kind share (ADR-0015).
type Collector struct {
	sambaConf   string
	exportsFile string
	hostID      string
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "shares" }

// Requires implements collector.Collector. Deliberately empty: the
// Registry's Requires() check is AND-only ("every listed capability must
// be present"), and a host with only Samba or only NFS is the common
// case, not the exception — this collector needs "at least one of", which
// Requires() alone can't express. It self-gates instead: parseSambaConf/
// parseExports fail silently on a missing file and Collect emits whatever
// combination of the two actually exists, including an empty (not
// missing) snapshot if neither is configured. See capabilities.ShareSMB
// and capabilities.ShareNFS in the manifest for whether either is present.
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

// Collect implements collector.Collector. It emits one Inventory of kind
// share combining whichever of SMB/NFS is actually present — a host with
// only one of the two still gets a single, complete snapshot.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var items []schema.InventoryItem
	if smb, err := parseSambaConf(c.sambaConf); err == nil {
		items = append(items, smb...)
	}
	if nfs, err := parseExports(c.exportsFile); err == nil {
		items = append(items, nfs...)
	}

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryShare,
		ReportedAt: time.Now().UTC(),
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

// parseSambaConf reads smb.conf's INI-like format: [section] headers
// followed by "key = value" lines, one section per share.
func parseSambaConf(path string) ([]schema.InventoryItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []schema.InventoryItem
	var section string
	attrs := schema.Labels{}

	flush := func() {
		if section == "" || sambaMetaSections[strings.ToLower(section)] {
			return
		}
		attrs["protocol"] = "smb"
		items = append(items, schema.InventoryItem{ID: section, Name: section, Attrs: attrs})
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = strings.TrimSpace(line[1 : len(line)-1])
			attrs = schema.Labels{}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "path":
			attrs["path"] = value
		case "public", "guest ok":
			attrs["mode"] = visibilityMode(value)
		case "read only", "writeable", "writable":
			// Only set writable if "mode" (visibility) hasn't already
			// decided it's public — read-only-ness is about write
			// access, orthogonal to who can connect, but both end up
			// informing the same "mode" summary attribute when public
			// wasn't explicitly set.
			if _, ok := attrs["writable"]; !ok {
				attrs["writable"] = strconv.FormatBool(isWritable(key, value))
			}
		}
	}
	flush()
	return items, scanner.Err()
}

func visibilityMode(value string) string {
	if isYes(value) {
		return "public"
	}
	return "private"
}

func isWritable(key, value string) bool {
	yes := isYes(value)
	if key == "read only" {
		return !yes
	}
	return yes
}

func isYes(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "1":
		return true
	default:
		return false
	}
}

// parseExports reads /etc/exports: one export per non-comment line, path
// first, then one or more client(options) entries.
func parseExports(path string) ([]schema.InventoryItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []schema.InventoryItem
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		exportPath := fields[0]
		mode := "private"
		writable := false
		for _, clientSpec := range fields[1:] {
			client, opts, _ := strings.Cut(clientSpec, "(")
			if client == "*" {
				mode = "public"
			}
			if strings.Contains(opts, "rw") {
				writable = true
			}
		}
		items = append(items, schema.InventoryItem{
			ID:   exportPath,
			Name: filepath.Base(exportPath),
			Attrs: schema.Labels{
				"path":     exportPath,
				"protocol": "nfs",
				"mode":     mode,
				"writable": strconv.FormatBool(writable),
			},
		})
	}
	return items, scanner.Err()
}
