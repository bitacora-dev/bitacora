// Package network implements two ADR-0015/0016 signals: per-interface
// traffic (a Metric — it's genuinely a time series) and VPN tunnel status
// (an Inventory — a list of tunnels, not a series). Tunnel status isn't
// read directly: WireGuard and Tailscale both need privilege this
// project's agent deliberately doesn't have, so it comes from
// bitacora-vpn's spool entry (ADR-0005), the same helper-writes/
// agent-reads split bitacora-smart established.
//
// Traffic is reported as raw cumulative byte counters straight from
// /proc/net/dev, not as a pre-computed rate: ADR-0006's allowedUnitSuffixes
// only recognizes "_total" (and the other SI-derived suffixes) as a public
// naming contract, and "_bytes_per_second" satisfies none of them, so a
// gauge named that way is silently rejected by schema.Metric.Validate and
// never reaches storage. A "_total" counter is also the Prometheus-idiomatic
// shape for this kind of value; the rate is derived hub-side from
// consecutive samples instead (see hubapi's rateSeries).
package network

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
	"github.com/bitacora-dev/bitacora/internal/vpnhelper"
)

const (
	defaultProcNetDev  = "/proc/net/dev"
	defaultSpoolDir    = "/var/lib/bitacora/spool"
	defaultSysClassNet = "/sys/class/net"
	// vpnReportInterval keeps the VPN tunnel Inventory on the cadence it
	// had when this collector ran every 30s. Traffic now runs at 10s (see
	// cmd/bitacora-agent), and re-reporting an unchanged tunnel list three
	// times as often would be pure write amplification: the helper that
	// produces it only runs every few minutes (ADR-0005) anyway.
	vpnReportInterval = 30 * time.Second
	// staleAfter mirrors ADR-0005's "más de tres intervalos de antigüedad
	// se descarta" — bitacora-vpn's timer interval is expected to be
	// short (minutes), so a stale VPN spool entry means the helper itself
	// has stopped running, not that nothing changed.
	staleAfter = 30 * time.Minute
)

// Collector emits net traffic counters and a vpn_tunnel Inventory. Traffic
// collection is stateless: each cycle reads and reports the current
// cumulative counters, with no previous-sample bookkeeping — the hub, not
// the agent, turns consecutive counter samples into a rate.
type Collector struct {
	procNetDev  string
	spoolDir    string
	sysClassNet string
	hostID      string

	now func() time.Time

	// The runtime abandons a Collect call that overruns its timeout but
	// cannot stop the goroutine running it (ADR-0007), so two cycles can
	// briefly overlap. mu guards the only state this collector keeps.
	mu            sync.Mutex
	lastVPNReport time.Time
}

type ifaceCounters struct {
	rxBytes, txBytes uint64
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{now: time.Now} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "network" }

// Requires implements collector.Collector. /proc/net/dev is always
// present on Linux; VPN tunnel data degrades gracefully to an empty
// Inventory when bitacora-vpn hasn't reported anything, so no capability
// gate applies here either.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.procNetDev = configuredPath(cfg, "proc_net_dev", defaultProcNetDev)
	c.spoolDir = configuredPath(cfg, "spool_dir", defaultSpoolDir)
	c.sysClassNet = configuredPath(cfg, "sys_class_net", defaultSysClassNet)
	if c.now == nil {
		c.now = time.Now
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

	now := c.clock()
	c.collectTraffic(sink)

	if c.shouldReportVPN(now) {
		items := c.readVPNTunnels()
		sink.Inventory(schema.Inventory{
			HostID:     c.hostID,
			Kind:       schema.InventoryVPNTunnel,
			ReportedAt: now.UTC(),
			Schema:     schema.CurrentSchemaVersion,
			Items:      items,
		})
	}

	return nil
}

func (c *Collector) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// shouldReportVPN throttles the tunnel Inventory to vpnReportInterval so
// shortening the traffic cadence doesn't silently change how often the VPN
// signal is written. The first cycle always reports.
func (c *Collector) shouldReportVPN(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastVPNReport.IsZero() && now.Sub(c.lastVPNReport) < vpnReportInterval {
		return false
	}
	c.lastVPNReport = now
	return true
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func configuredPath(cfg collector.Config, key, fallback string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// collectTraffic reports the current cumulative byte counters of the
// interfaces that count as host traffic (see selectHostInterfaces) as-is.
// There's no rate math and no previous-cycle state here: emitting the raw
// counter means the very first cycle already produces a sample (no warm-up
// cycle needed to establish a delta), and a counter reset from a reboot or
// NIC reset is simply a lower next value — it's the hub's job to notice
// that when it differentiates, not this collector's.
func (c *Collector) collectTraffic(sink collector.Sink) {
	counters, err := readProcNetDev(c.procNetDev)
	if err != nil {
		return
	}

	names := make([]string, 0, len(counters))
	for iface := range counters {
		names = append(names, iface)
	}
	for _, iface := range selectHostInterfaces(names, c.sysClassNet) {
		cur := counters[iface]
		sink.Counter("bitacora_net_rx_bytes_total", float64(cur.rxBytes), collector.Labels{"interface": iface})
		sink.Counter("bitacora_net_tx_bytes_total", float64(cur.txBytes), collector.Labels{"interface": iface})
	}
}

// selectHostInterfaces decides which of the interfaces in /proc/net/dev
// count as this host's traffic. Answering "all of them" is what made the
// number false: on a Docker host the same byte is counted two and three
// times, because a container's packet crosses its veth, then the bridge
// (docker0, docker_gwbridge, br-*), then the physical NIC. Tunnels double
// count the same way: a byte sent over tailscale0 or wg0 leaves the
// machine encapsulated inside a physical NIC's counters too.
//
// The rule is therefore: count only the interfaces bytes actually cross to
// enter or leave the machine — the device-backed ones. That is also the
// only stable set. veth interfaces are created and destroyed with every
// container start and stop, so including them churns series identities
// (ADR-0006's cardinality concern) for numbers that were already counted
// elsewhere.
//
// Two tiers, because sysfs is the authority but isn't always there:
//
//  1. If sysfs classifies at least one interface as device-backed, those
//     are the host's interfaces. This is the production path: sysfs links
//     every virtual netdev under .../devices/virtual/net/, so bridges,
//     veths, tunnels, bonds and VLANs all fall out here by construction,
//     including kinds that don't exist yet.
//  2. Otherwise — sysfs absent, or every interface virtual, which is what
//     an agent inside a container sees, where the container's own eth0 IS
//     a veth end and dropping it would report nothing — fall back to
//     excluding the well-known container and tunnel plumbing by name.
//
// Loopback is already filtered out while parsing /proc/net/dev.
func selectHostInterfaces(names []string, sysClassNet string) []string {
	sort.Strings(names)

	deviceBacked := make([]string, 0, len(names))
	for _, iface := range names {
		if isDeviceBacked(sysClassNet, iface) {
			deviceBacked = append(deviceBacked, iface)
		}
	}
	if len(deviceBacked) > 0 {
		return deviceBacked
	}

	selected := make([]string, 0, len(names))
	for _, iface := range names {
		if !looksLikeVirtualInterface(iface) {
			selected = append(selected, iface)
		}
	}
	return selected
}

// isDeviceBacked asks sysfs whether an interface is backed by a real
// device. Linux symlinks /sys/class/net/<iface> into the device tree, and
// every software netdev lands under .../devices/virtual/net/ — so the
// check is a readlink, not a name pattern, and needs no exec (ADR-0012).
// A missing or unreadable link answers "unknown", which reads as
// not-device-backed and lets the name fallback decide.
func isDeviceBacked(sysClassNet, iface string) bool {
	if sysClassNet == "" {
		return false
	}
	target, err := os.Readlink(filepath.Join(sysClassNet, iface))
	if err != nil {
		return false
	}
	return !strings.Contains(filepath.ToSlash(target), "/devices/virtual/")
}

// virtualInterfacePrefixes lists the interface name prefixes that are
// never host traffic in their own right, used only when sysfs can't
// answer. Each entry is either container/hypervisor plumbing whose bytes
// are also counted on the NIC they exit through (veth, docker0,
// docker_gwbridge, br-*, virbr*, lxcbr*, cni*, flannel*, vnet*, tap*), an
// encapsulating tunnel whose payload leaves inside a physical NIC's
// counters (tun*, wg*, tailscale*, zt*, vxlan*, gre*, sit*), or an
// aggregate that would double count its own members (bond*, dummy*).
var virtualInterfacePrefixes = []string{
	"veth", "docker", "virbr", "lxcbr", "cni", "flannel", "vnet", "tap",
	"tun", "wg", "tailscale", "zt", "vxlan", "gre", "sit",
	"bond", "dummy",
}

func looksLikeVirtualInterface(iface string) bool {
	for _, prefix := range virtualInterfacePrefixes {
		if strings.HasPrefix(iface, prefix) {
			return true
		}
	}
	// Bridges: "br-<hash>" is Docker's user-defined network naming and
	// "br0"/"br1" the conventional manual one. Plain "br" as a whole name
	// is not a bridge convention, so a bare prefix match would be too
	// eager — a separator or digit has to follow.
	if rest, ok := strings.CutPrefix(iface, "br"); ok && rest != "" {
		if rest[0] == '-' || (rest[0] >= '0' && rest[0] <= '9') {
			return true
		}
	}
	return false
}

// readProcNetDev parses /proc/net/dev's fixed column format: two header
// lines, then "iface: rx_bytes rx_packets rx_errs rx_drop rx_fifo
// rx_frame rx_compressed rx_multicast tx_bytes tx_packets ..." — only the
// first (rx_bytes) and ninth (tx_bytes) numeric fields matter here.
// Loopback is excluded: its "traffic" is never a signal worth graphing.
func readProcNetDev(path string) (map[string]ifaceCounters, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	counters := map[string]ifaceCounters{}
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // the two header lines
		}
		line := scanner.Text()
		iface, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		iface = strings.TrimSpace(iface)
		if iface == "lo" || iface == "" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		counters[iface] = ifaceCounters{rxBytes: rx, txBytes: tx}
	}
	return counters, scanner.Err()
}

// readVPNTunnels reads bitacora-vpn's spool entry and parses whichever of
// WireGuard/Tailscale it captured. A missing or stale entry (helper never
// ran, or stopped running) yields no items, not an error — VPN tunnels
// are an optional signal, same as every other ADR-0015 addition.
func (c *Collector) readVPNTunnels() []schema.InventoryItem {
	entries, err := spool.ReadDir(c.spoolDir)
	if err != nil {
		return nil
	}
	entry, ok := entries["vpn"]
	// Entry.Stale(now, interval) treats "interval" as the collector's own
	// cadence and multiplies it by 3 internally (ADR-0005) — staleAfter
	// here is already the absolute threshold, so it's compared to Age
	// directly rather than passed as that interval.
	if !ok || entry.Age(time.Now()) > staleAfter {
		return nil
	}

	var result vpnhelper.Result
	if err := json.Unmarshal(entry.Data, &result); err != nil {
		return nil
	}

	var items []schema.InventoryItem
	items = append(items, parseWireguardDump(result.WireguardDump)...)
	if item, ok := parseTailscaleStatus(result.TailscaleStatusJSON); ok {
		items = append(items, item)
	}
	return items
}

// parseWireguardDump parses `wg show all dump`'s tab-separated format
// (wg(8)): an interface line ("iface priv-key pub-key listen-port
// fwmark", 5 fields) followed by one line per peer ("iface pub-key
// preshared-key endpoint allowed-ips latest-handshake rx tx keepalive",
// 9 fields).
func parseWireguardDump(dump string) []schema.InventoryItem {
	if dump == "" {
		return nil
	}

	var items []schema.InventoryItem
	scanner := bufio.NewScanner(strings.NewReader(dump))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 9 {
			continue // interface line (5 fields), or malformed — either way, not a peer
		}
		iface := fields[0]
		peerPubKey := fields[1]
		endpoint := fields[3]
		handshakeUnix, _ := strconv.ParseInt(fields[5], 10, 64)

		active := "false"
		lastSeen := ""
		if handshakeUnix > 0 {
			t := time.Unix(handshakeUnix, 0).UTC()
			lastSeen = t.Format(time.RFC3339)
			// wg considers a handshake within the last ~3 minutes "active"
			// (its own rekey interval); anything older means the tunnel
			// hasn't exchanged a handshake recently, which is the closest
			// available proxy for "is this peer actually connected".
			if time.Since(t) < 3*time.Minute {
				active = "true"
			}
		}

		items = append(items, schema.InventoryItem{
			ID:   iface + "/" + shortKey(peerPubKey),
			Name: iface,
			Attrs: schema.Labels{
				"protocol":       "wireguard",
				"peer":           shortKey(peerPubKey),
				"endpoint":       endpoint,
				"active":         active,
				"last_handshake": lastSeen,
			},
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// shortKey truncates a WireGuard base64 public key for display — the
// full key isn't secret (only the private key is), but a 44-character
// string is noise in a summary view.
func shortKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12] + "…"
}

// parseTailscaleStatus reads `tailscale status --json`'s output
// leniently: only a couple of well-known top-level fields, tolerant of
// whatever else the real schema contains — Tailscale's JSON output is
// richer and more version-sensitive than WireGuard's dump format, so this
// is deliberately best-effort rather than a full structured parse.
func parseTailscaleStatus(raw string) (schema.InventoryItem, bool) {
	if raw == "" {
		return schema.InventoryItem{}, false
	}

	var status struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			HostName string `json:"HostName"`
			Online   bool   `json:"Online"`
		} `json:"Self"`
	}
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return schema.InventoryItem{}, false
	}

	return schema.InventoryItem{
		ID:   "tailscale",
		Name: "tailscale",
		Attrs: schema.Labels{
			"protocol": "tailscale",
			"active":   strconv.FormatBool(status.Self.Online),
			"state":    status.BackendState,
		},
	}, true
}
