# internal/collector/network

Two ADR-0015/0016 signals in one collector:

- **Traffic per interface** (`Metric`): reads `/proc/net/dev`'s fixed
  column format, computes a bytes/second rate from two consecutive
  samples (same delta pattern as `internal/collector/cpu`). Loopback is
  excluded — it's never a meaningful signal. A counter that goes
  backwards between samples (an interface reset) skips that one cycle
  rather than reporting a nonsense rate.
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
