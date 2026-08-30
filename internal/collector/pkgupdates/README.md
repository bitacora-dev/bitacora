# internal/collector/pkgupdates

Pending-updates collector (ADR-0017): what packages, UnRaid plugins and
Docker images a host has installed that have a newer version available.
Emits a single Inventory of kind `package_update`, one item per outdated
thing found — a system that's fully up to date reports an empty (not
missing) snapshot, not hundreds of "unchanged" entries.

Four independent sources, each degrading on its own:

- **apt** (`apt.go`): pure read of `/var/lib/dpkg/status` (installed
  versions) and apt's own local metadata cache
  (`/var/lib/apt/lists/*_Packages`, the same cache `apt update`
  maintains) — no `exec`. Versions are compared with Debian's own
  ordering rules (`internal/debversion`), not plain string comparison,
  so "1.9" is correctly older than "1.10". Reports `cache_age_seconds`
  (the oldest `*_Packages` file's age) alongside each result — a stale
  `apt update` means a stale answer, and that's surfaced, not hidden.
- **dnf** (`dnf.go`): reads the spool entry the new `bitacora-dnf`
  helper writes after running `dnf check-update` (ADR-0005) — parsing
  DNF's repository metadata format directly isn't reasonable without a
  real dependency, so a privileged helper runs the one closed query
  ADR-0012 allows instead.
- **UnRaid plugins** (`unraid.go`): reads each installed plugin's local
  `.plg` file and fetches the same file from its own declared
  `pluginURL` to compare versions — UnRaid keeps no local "what's
  available" cache, so each plugin is its own network round-trip.
  Real `.plg` files often declare their attributes as DOCTYPE `<!ENTITY>`
  macros rather than literal values; `parsePLG` resolves those itself
  rather than pulling in a full XML+DTD parser for three fields.
- **Docker images** (`docker.go`, `registry.go`): reads locally present
  images via `docker-socket-proxy` (never the Docker socket directly,
  ADR-0005) and checks each one's digest against its registry using the
  standard OCI/Docker Registry v2 distribution API — the same
  auth-challenge flow (`HEAD` manifest → 401 with `WWW-Authenticate` →
  fetch a bearer token → retry) Docker Hub, GHCR and most self-hosted
  registries all implement, so this isn't Docker-Hub-specific.

All four write into the SAME Inventory kind rather than four separate
ones: ADR-0017 names one `PackageUpdates` interface, and Inventory's
replace-per-`(host, kind)` semantics means separate kinds would each
silently need their own query — see `internal/schema.InventoryShareUsage`
for the same tradeoff made the other way, where the cadences genuinely
differ enough to justify a split.

`Requires()` returns nil: every source degrades independently (missing
dpkg status, no dnf spool entry, no UnRaid plugins directory, no
`docker_socket_proxy_url` configured), so no single capability gates the
whole collector.

## What's NOT here

- **Per-package publish dates.** ADR-0017 explicitly allows showing only
  the version jump when the date a candidate version was published isn't
  knowable from what's already being read — getting that would mean a
  second network request per outdated package (changelogs, release
  APIs), which this deliberately doesn't do.
- **Registries that require real credentials.** A private registry that
  demands more than anonymous or scope-limited token auth ends up
  "couldn't check", not authenticated against — ADR-0017 explicitly
  accepts this rather than forcing credential configuration.
- **`Obsoleting Packages`** in dnf's output — packages that outright
  replace another installed package are a different concept from a
  version update, and `dnfhelper.parseCheckUpdate` stops before that
  section rather than trying to interpret its different format.
