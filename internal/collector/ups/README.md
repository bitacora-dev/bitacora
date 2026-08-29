# internal/collector/ups

SAI/UPS inventory collector (ADR-0015, ADR-0016) via NUT (Network UPS
Tools): a simple, line-based ASCII protocol over TCP (`upsd`, default
port 3493) — no privilege needed, just a socket, unlike WireGuard/
Tailscale.

- `LIST UPS` to enumerate configured UPS names, then `LIST VAR <name>`
  per UPS for `ups.status`, `battery.charge`, `battery.runtime`,
  `ups.model`.
- `on_battery` is derived from whether `ups.status` contains `OB` (NUT's
  own status-flag convention — statuses combine, e.g. `"OB LB"` means on
  battery *and* low battery).
- A NUT server that's unreachable (not installed, not running) yields an
  empty snapshot, not an error.

Tested against a real NUT protocol implementation (an in-process fake
`upsd` speaking the real wire format over a real TCP socket), not a
mocked client.

## What's NOT here

- **apcupsd.** ADR-0016 names it as the alternative to NUT, but its NIS
  protocol uses length-prefixed binary framing, not NUT's line-based
  text — a different enough protocol that implementing it alongside NUT
  in the same task risked doing both shallowly. A host running apcupsd
  instead of NUT gets an empty UPS snapshot today; this is the one
  documented gap in this task's `power.ups` coverage.
