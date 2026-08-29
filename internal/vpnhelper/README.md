# internal/vpnhelper

Logic for `cmd/bitacora-vpn`, the second privileged helper after
`bitacora-smart` (ADR-0005, ADR-0016): WireGuard and Tailscale both need
privilege this project's agent deliberately doesn't have, so a short-lived
root helper reads their status and writes it to the spool for the
unprivileged `internal/collector/network` to parse and interpret later.

Calls exactly two commands, both closed-list with no arguments derived
from anything external (ADR-0012): `wg show all dump` and
`tailscale status --json`. Command execution is injected (`CommandRunner`)
so this is testable without either program installed — same pattern
`internal/smarthelper` uses for `smartctl`.

Deliberately captures raw, unparsed output rather than parsing here: the
helper's only job is capturing safely; interpreting WireGuard's
tab-separated dump and Tailscale's JSON happens unprivileged, in
`internal/collector/network`.
