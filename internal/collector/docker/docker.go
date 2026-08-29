// Package docker implements the Docker container collector (ADR-0005,
// ADR-0007): resource metrics come straight from cgroup v2, which needs
// no privilege at all (ADR-0005: "no requiere permiso alguno, y además es
// más barato que consultar la API"). Container names come from
// docker-socket-proxy, best-effort — without it, the collector runs in
// ADR-0005's explicit degraded mode: resource metrics yes, names no.
package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
)

// DefaultCgroupRoot is where systemd puts container scopes on a
// docker.service-managed host.
const DefaultCgroupRoot = "/sys/fs/cgroup/system.slice"

// Collector emits per-container CPU and memory usage from cgroup v2.
type Collector struct {
	cgroupRoot string
	metadata   *MetadataClient // nil when no docker-socket-proxy is configured
}

// New returns a collector reading the real cgroup v2 hierarchy.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "docker" }

// Requires implements collector.Collector. Only cgroupv2 is a hard
// requirement — metadata via docker-socket-proxy is best-effort within
// Collect, not a registration gate, per ADR-0005's degraded-mode design.
func (c *Collector) Requires() []collector.Capability {
	return []collector.Capability{capabilities.ContainerCgroupv2}
}

// Init implements collector.Collector. cfg["cgroup_root"] overrides the
// cgroup v2 root (tests point it at testdata/); cfg["docker_socket_proxy_url"],
// if set, enables container-name lookups.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.cgroupRoot = DefaultCgroupRoot
	if v, ok := cfg["cgroup_root"].(string); ok && v != "" {
		c.cgroupRoot = v
	}
	if v, ok := cfg["docker_socket_proxy_url"].(string); ok && v != "" {
		c.metadata = NewMetadataClient(v)
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

	ids, err := listContainerScopes(c.cgroupRoot)
	if err != nil {
		return fmt.Errorf("listing container cgroup scopes: %w", err)
	}

	var names map[string]string
	if c.metadata != nil {
		// Best-effort: metadata unavailability degrades labels, it never
		// fails the collector (ADR-0005).
		names, _ = c.metadata.ContainerNames(ctx)
	}

	for _, id := range ids {
		scopeDir := filepath.Join(c.cgroupRoot, "docker-"+id+".scope")
		labels := labelsFor(id, names)

		if usageSeconds, err := readCPUUsageSeconds(scopeDir); err == nil {
			sink.Counter("bitacora_container_cpu_seconds_total", usageSeconds, labels)
		}
		if memBytes, err := readMemoryCurrent(scopeDir); err == nil {
			sink.Gauge("bitacora_container_memory_bytes", float64(memBytes), labels)
		}
	}

	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

// labelsFor builds the container_id/container_name label pair, truncating
// the ID to 12 characters (ADR-0006) and falling back to the truncated ID
// as the name when no metadata is available — schema.Metric.Validate
// requires container_name whenever container_id is present, and "the ID
// itself" is a defensible, technically-valid fallback for degraded mode
// rather than omitting per-container labels entirely.
func labelsFor(fullID string, names map[string]string) collector.Labels {
	short := fullID
	if len(short) > 12 {
		short = short[:12]
	}
	name := names[fullID]
	if name == "" {
		name = short
	}
	return collector.Labels{"container_id": short, "container_name": name}
}

// listContainerScopes returns the full container IDs found as
// docker-<id>.scope directories under root.
func listContainerScopes(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no containers running, or cgroup not mounted here
		}
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "docker-") && strings.HasSuffix(name, ".scope") {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "docker-"), ".scope")
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func readCPUUsageSeconds(scopeDir string) (float64, error) {
	raw, err := os.ReadFile(filepath.Join(scopeDir, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			usec, err := strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return 0, fmt.Errorf("parsing usage_usec: %w", err)
			}
			return usec / 1e6, nil
		}
	}
	return 0, fmt.Errorf("usage_usec not found in cpu.stat")
}

func readMemoryCurrent(scopeDir string) (int64, error) {
	raw, err := os.ReadFile(filepath.Join(scopeDir, "memory.current"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}
