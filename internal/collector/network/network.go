// Package network implements two ADR-0015/0016 signals: per-interface
// traffic (a Metric — it's genuinely a time series) and VPN tunnel status
// (an Inventory — a list of tunnels, not a series). Tunnel status isn't
// read directly: WireGuard and Tailscale both need privilege this
// project's agent deliberately doesn't have, so it comes from
// bitacora-vpn's spool entry (ADR-0005), the same helper-writes/
// agent-reads split bitacora-smart established.
package network

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
	"github.com/bitacora-dev/bitacora/internal/vpnhelper"
)

const (
	defaultProcNetDev = "/proc/net/dev"
	defaultSpoolDir   = "/var/lib/bitacora/spool"
	// staleAfter mirrors ADR-0005's "más de tres intervalos de antigüedad
	// se descarta" — bitacora-vpn's timer interval is expected to be
	// short (minutes), so a stale VPN spool entry means the helper itself
	// has stopped running, not that nothing changed.
	staleAfter = 30 * time.Minute
)

// Collector emits net traffic gauges and a vpn_tunnel Inventory.
type Collector struct {
	procNetDev string
	spoolDir   string
	hostID     string

	prevAt   time.Time
	prevInts map[string]ifaceCounters
}

type ifaceCounters struct {
	rxBytes, txBytes uint64
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

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
	c.collectTraffic(sink, now)

	items := c.readVPNTunnels()
	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryVPNTunnel,
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

func (c *Collector) collectTraffic(sink collector.Sink, now time.Time) {
	counters, err := readProcNetDev(c.procNetDev)
	if err != nil {
		return
	}

	if !c.prevAt.IsZero() {
		elapsed := now.Sub(c.prevAt).Seconds()
		if elapsed > 0 {
			for iface, cur := range counters {
				prev, ok := c.prevInts[iface]
				if !ok || cur.rxBytes < prev.rxBytes || cur.txBytes < prev.txBytes {
					continue // new interface, or a counter reset — skip this cycle, not an error
				}
				rxRate := float64(cur.rxBytes-prev.rxBytes) / elapsed
				txRate := float64(cur.txBytes-prev.txBytes) / elapsed
				sink.Gauge("bitacora_net_rx_bytes_per_second", rxRate, collector.Labels{"interface": iface})
				sink.Gauge("bitacora_net_tx_bytes_per_second", txRate, collector.Labels{"interface": iface})
			}
		}
	}

	c.prevInts = counters
	c.prevAt = now
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
