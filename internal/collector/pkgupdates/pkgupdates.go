// Package pkgupdates implements ADR-0017's PackageUpdates interface
// (originally named in ADR-0004): what packages, UnRaid plugins and
// Docker images a host has installed that have a newer version
// available. Four sources, four levels of difficulty:
//
//   - apt: pure read of /var/lib/dpkg/status and apt's own local
//     metadata cache, compared with Debian's own version semantics
//     (internal/debversion) — no exec, no privilege.
//   - dnf: reads the spool entry bitacora-dnf (a new privileged helper,
//     ADR-0005) writes after running `dnf check-update`.
//   - UnRaid plugins: reads each installed plugin's local .plg file and
//     fetches the same file from its own declared source to compare.
//   - Docker images: reads locally present images via
//     docker-socket-proxy and checks each one's digest against its
//     registry over the standard OCI/Docker Registry v2 API.
//
// All four are deliberately one Inventory kind (package_update), not
// four separate ones: ADR-0017 names a single PackageUpdates interface,
// and Inventory's replace-per-(host,kind) semantics means four separate
// kinds would each need their own endpoint/query rather than one "what
// needs attention" list — the same reasoning
// internal/collector/hwidentity uses for combining unrelated attributes
// under one Requires()-less collector.
package pkgupdates

import (
	"context"
	"net/http"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultDpkgStatus       = "/var/lib/dpkg/status"
	defaultAptListsDir      = "/var/lib/apt/lists"
	defaultSpoolDir         = "/var/lib/bitacora/spool"
	defaultUnraidPluginsDir = "/boot/config/plugins"
)

// Collector emits an Inventory of kind package_update (ADR-0017).
type Collector struct {
	dpkgStatus        string
	aptListsDir       string
	spoolDir          string
	unraidPluginsDir  string
	dockerMetadataURL string
	httpClient        *http.Client
	registry          *registryClient
	hostID            string
}

// New returns a collector with production defaults.
func New() *Collector {
	client := &http.Client{Timeout: 10 * time.Second}
	return &Collector{httpClient: client, registry: &registryClient{HTTPClient: client}}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "pkgupdates" }

// Requires implements collector.Collector. Every source degrades
// independently (no dpkg status, no dnf spool entry, no UnRaid plugins
// dir, no docker-socket-proxy configured) — no single capability gates
// the whole thing, the same reasoning internal/collector/hwidentity uses.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector. cfg["docker_socket_proxy_url"],
// if set, enables the Docker image source — without it, that source is
// silently skipped, same degraded-mode design as
// internal/collector/docker's own container-name lookups.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.dpkgStatus = configuredPath(cfg, "dpkg_status", defaultDpkgStatus)
	c.aptListsDir = configuredPath(cfg, "apt_lists_dir", defaultAptListsDir)
	c.spoolDir = configuredPath(cfg, "spool_dir", defaultSpoolDir)
	c.unraidPluginsDir = configuredPath(cfg, "unraid_plugins_dir", defaultUnraidPluginsDir)
	if v, ok := cfg["docker_socket_proxy_url"].(string); ok {
		c.dockerMetadataURL = v
	}
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
	var items []schema.InventoryItem
	items = append(items, aptItems(c.dpkgStatus, c.aptListsDir, now)...)
	items = append(items, dnfItems(c.spoolDir)...)
	items = append(items, unraidItems(ctx, c.unraidPluginsDir, c.httpClient)...)
	items = append(items, dockerItems(ctx, c.dockerMetadataURL, c.registry)...)

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryPackageUpdate,
		ReportedAt: now.UTC(),
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
