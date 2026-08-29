// Package diskarray implements the per-disk storage breakdown ADR-0016
// asks for: instead of one global percentage, one Inventory item per real
// mounted filesystem — capacity/used/available via statfs, plus model and
// serial number when a matching entry exists in bitacora-smart's spool
// (ADR-0005). It doesn't try to know which disks belong to which named
// array (mdraid, SnapRAID, UnRaid) — each disk is reported independently,
// identified by its own mountpoint and device.
package diskarray

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

const (
	defaultMountsFile = "/proc/mounts"
	defaultSpoolDir   = "/var/lib/bitacora/spool"
)

// pseudoFSTypes are never real disks worth reporting.
var pseudoFSTypes = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "tmpfs": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true,
	"devpts": true, "mqueue": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true,
	"binfmt_misc": true, "hugetlbfs": true, "configfs": true,
	"fusectl": true, "nsfs": true, "efivarfs": true, "rpc_pipefs": true,
}

// Collector emits an Inventory of kind disk (ADR-0016).
type Collector struct {
	mountsFile string
	spoolDir   string
	hostID     string
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "diskarray" }

// Requires implements collector.Collector. Every real host has at least
// a root filesystem to report — no capability gate needed.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.mountsFile = configuredPath(cfg, "mounts_file", defaultMountsFile)
	c.spoolDir = configuredPath(cfg, "spool_dir", defaultSpoolDir)
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

	mounts, err := readRealMounts(c.mountsFile)
	if err != nil {
		mounts = nil
	}
	smart := c.readSMARTIdentities()

	items := make([]schema.InventoryItem, 0, len(mounts))
	for _, m := range mounts {
		attrs := schema.Labels{"device": m.device, "fstype": m.fsType}

		if usage, ok := statfsUsage(m.mountpoint); ok {
			attrs["capacity_bytes"] = strconv.FormatUint(usage.total, 10)
			attrs["used_bytes"] = strconv.FormatUint(usage.used, 10)
			attrs["available_bytes"] = strconv.FormatUint(usage.available, 10)
		}

		if id, ok := smart[baseDeviceName(m.device)]; ok {
			if id.model != "" {
				attrs["model"] = id.model
			}
			if id.serial != "" {
				attrs["serial"] = id.serial
			}
		}

		items = append(items, schema.InventoryItem{ID: m.mountpoint, Name: m.mountpoint, Attrs: attrs})
	}

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryDisk,
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

type mountEntry struct {
	device     string
	mountpoint string
	fsType     string
}

// readRealMounts parses /proc/mounts' fixed 6-field-per-line format
// ("device mountpoint fstype options dump pass"), decoding the octal
// \NNN escapes the kernel uses for spaces and other special characters
// in paths, and dropping every pseudo/virtual filesystem.
func readRealMounts(path string) ([]mountEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []mountEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device, mountpoint, fsType := fields[0], unescapeMountField(fields[1]), fields[2]
		if pseudoFSTypes[fsType] || !strings.HasPrefix(device, "/dev/") {
			continue
		}
		entries = append(entries, mountEntry{device: device, mountpoint: mountpoint, fsType: fsType})
	}
	return entries, scanner.Err()
}

func unescapeMountField(s string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(s)
}

type diskUsage struct {
	total, used, available uint64
}

func statfsUsage(mountpoint string) (diskUsage, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(mountpoint, &st); err != nil {
		return diskUsage{}, false
	}
	bsize := uint64(st.Bsize)
	total := uint64(st.Blocks) * bsize
	free := uint64(st.Bfree) * bsize
	avail := uint64(st.Bavail) * bsize
	if total < free {
		return diskUsage{}, false
	}
	return diskUsage{total: total, used: total - free, available: avail}, true
}

// baseDeviceName strips a trailing partition number so "/dev/sdc1" and
// "/dev/nvme0n1p1" both match the whole-disk name bitacora-smart reports
// SMART data under ("sdc", "nvme0n1") — the name /sys/block enumerates
// and smarthelper.DeviceLister queries.
//
// This only strips a suffix for the two naming schemes that actually have
// a distinct partition suffix: "sdX<N>" (letters, then a trailing
// partition number) and "nvme<N>n<N>p<N>" (a "pN" partition suffix after
// the whole-disk "nvme0n1" part). Every other scheme this project might
// see — "mdN", "dm-N", "loopN" — has its trailing digits as part of the
// whole-disk name itself, not a partition, and must be left untouched:
// naively stripping any trailing digit run would turn "nvme0n1" into
// "nvme0n" and "md0" into "md", neither of which anything is keyed under.
func baseDeviceName(devicePath string) string {
	name := strings.TrimPrefix(devicePath, "/dev/")

	if strings.HasPrefix(name, "nvme") {
		if idx := strings.LastIndex(name, "p"); idx > 0 {
			if _, err := strconv.Atoi(name[idx+1:]); err == nil {
				return name[:idx]
			}
		}
		return name
	}

	if strings.HasPrefix(name, "sd") {
		end := len(name)
		for end > 0 && name[end-1] >= '0' && name[end-1] <= '9' {
			end--
		}
		if end > 2 { // keep at least "sd" + one letter, e.g. "sda"
			return name[:end]
		}
	}

	return name
}

type smartIdentity struct {
	model  string
	serial string
}

// readSMARTIdentities reads bitacora-smart's spool entry (ADR-0005) and
// extracts model/serial from each device's raw smartctl --json output —
// leniently: only the couple of well-known top-level fields this needs,
// tolerant of whatever else smartctl's much larger real schema contains.
func (c *Collector) readSMARTIdentities() map[string]smartIdentity {
	entries, err := spool.ReadDir(c.spoolDir)
	if err != nil {
		return nil
	}
	entry, ok := entries["smart"]
	if !ok {
		return nil
	}

	var result struct {
		Devices map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(entry.Data, &result); err != nil {
		return nil
	}

	identities := make(map[string]smartIdentity, len(result.Devices))
	for device, raw := range result.Devices {
		var parsed struct {
			ModelName    string `json:"model_name"`
			SerialNumber string `json:"serial_number"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			continue
		}
		identities[device] = smartIdentity{model: parsed.ModelName, serial: parsed.SerialNumber}
	}
	return identities
}
