# internal/collector/network

Two ADR-0015/0016 signals in one collector:

- **Traffic per interface** (`Metric`): reads `/proc/net/dev`'s fixed
  column format and reports the raw cumulative `_total` byte counters
  as-is, labelled by interface. The bytes/second rate is derived hub-side
  from consecutive samples (`hubapi`'s `rateSeries`), not here: ADR-0006's
  naming contract only recognizes `_total` and the other SI-derived
  suffixes, so a `_bytes_per_second` gauge is rejected by
  `schema.Metric.Validate` and never reaches storage. Reporting the raw
  counter also means the first cycle already produces a sample, and a
  counter reset is just a lower next value for the hub to notice.

- **VPN tunnel status** (`Inventory` of kind `vpn_tunnel`): WireGuard and
  Tailscale both need privilege this project's agent deliberately doesn't
  have, so this reads `bitacora-vpn`'s spool entry (ADR-0005) instead of
  querying either directly — same helper-writes/agent-reads split
  `bitacora-smart` established. A missing or stale (>30 min old) entry
  yields no items, not an error.
  - WireGuard: parses `wg show all dump`'s tab-separated format
    (documented in `wg(8)`) — one item per interface/peer pair. A peer is
    "active" if its last handshake was within the last 3 minutes (wg's
    own rekey interval, the closest available proxy for "connected").
  - Tailscale: parses `tailscale status --json` leniently — only
    `BackendState` and `Self.{HostName,Online}`, tolerant of whatever else
    the real (richer, more version-sensitive) schema contains.

### Which interfaces count as host traffic

Only the **device-backed** ones. Reporting every interface in
`/proc/net/dev` counts the same byte two and three times: a container's
packet crosses its `veth`, then the bridge (`docker0`, `docker_gwbridge`,
`br-*`), then the physical NIC it leaves through. Tunnels double count the
same way — a byte sent over `tailscale0` or `wg0` also leaves inside a
physical NIC's counters. Those interfaces are also the unstable ones: a
`veth` is created and destroyed with every container start and stop, which
churns series identities for numbers already counted elsewhere (ADR-0006's
cardinality concern).

The classification asks sysfs, not the name: Linux symlinks
`/sys/class/net/<iface>` into the device tree and puts every software
netdev under `.../devices/virtual/net/`, so bridges, veths, tunnels, bonds
and VLANs fall out by construction — including kinds that don't exist yet.
It is a `readlink`, so it needs no privilege and no exec (ADR-0012).

When sysfs classifies *nothing* as device-backed — no sysfs, or an agent
running inside a container, where the container's own `eth0` is the far
end of a `veth` pair — the collector falls back to excluding well-known
container and tunnel plumbing by name rather than reporting nothing.
Reporting nothing would be the same lie as the `0 B/s` this replaced.

Loopback is filtered out while parsing `/proc/net/dev`, before either tier.

### Cadence

Traffic runs at 10s, the same as `cpu` and `memory`, so the derived rate
has the same resolution as the other two panels. The VPN inventory in the
same collector keeps its own 30s cadence (`vpnReportInterval`): the
privileged helper that produces it only runs every few minutes (ADR-0005),
so rewriting an unchanged tunnel list three times as often buys nothing.

## What's NOT here

- `bitacora-vpn` isn't wired into any systemd timer being *run* by this
  package — the unit files exist (`packaging/systemd/bitacora-vpn.*`),
  but nothing here starts it; that's an installation/packaging step per
  ADR-0005's existing model.
- Per-peer Tailscale tunnels — only one summary item for the whole
  daemon. Tailscale's mesh model (many peers, not one point-to-point
  tunnel) doesn't map as cleanly to "túneles" as WireGuard's peers do;
  expanding this to one item per Tailscale peer is a reasonable follow-up
  if it turns out to matter.
