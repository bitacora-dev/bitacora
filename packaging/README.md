# packaging

Privilege model artifacts for ADR-0005: not a full installer yet (that's
`nfpm`/`.deb`/`.rpm` packaging, a later task), just the pieces that
implement the model itself.

- `scripts/provision-user.sh` — creates the non-root `bitacora` system user,
  grants it `systemd-journal` membership, and creates
  `/var/lib/bitacora/spool` with the required `root:bitacora 0750`
  permissions. Idempotent, run as root.
- `systemd/bitacora-agent.service` — the main daemon's sandboxed unit,
  exactly as specified in ADR-0005. Never relax any of these settings
  without a matching ADR.
- `systemd/bitacora-smart.service` + `systemd/bitacora-smart.timer` — the
  first privileged helper: root, no network, dies within
  `RuntimeMaxSec=60`, runs every 15 minutes.
- `systemd/bitacora-vpn.service` + `systemd/bitacora-vpn.timer` — the
  second privileged helper (ADR-0016): root, `CAP_NET_ADMIN` for `wg`'s
  netlink queries, runs every 5 minutes. Unlike `bitacora-smart`, it does
  **not** set `PrivateNetwork=yes` — WireGuard interfaces are
  network-namespace scoped, so an isolated netns would see none of them.
- `rpm/` — AlmaLinux/RPM notes and the SELinux policy module source.
- `unraid/` — UnRaid plugin skeleton and rc.d service wrapper for hosts without
  systemd.

Check the result with `bita doctor`.
