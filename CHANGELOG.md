# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/) once
it ships a first tagged release.

## [Unreleased]

## [0.1.0] - 2026-08-30

First usable release: a read-only agent/hub pair that can be pointed at a
Debian/Ubuntu, AlmaLinux, UnRaid or generic Linux host and produce a
correlated timeline of metrics, events, logs and inventory — every ADR
through [ADR-0017](docs/adr/0017-actualizaciones-pendientes.md) is
implemented.

### Added

- Repository scaffolding: dual license (AGPL-3.0 core, Apache-2.0
  `bitacora-run`), architecture decision records, Go module layout for
  `bitacora-agent`, `bitacora-hub` and `bita`.
- Governance files required from day one per
  [ADR-0013](docs/adr/0013-nombre-licencia-y-gobernanza.md): this file,
  `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `.gitleaks.toml`,
  issue and PR templates.
- Canonical data model: `Metric`, `Event`, `LogLine` and `Inventory` (the
  fourth, declarative-list shape added in ADR-0015), the `Collector`
  interface and its runtime lifecycle.
- Capability manifest and detection (ADR-0004), covering Debian/Ubuntu,
  AlmaLinux, UnRaid and a generic-VPS host.
- Storage: SQLite and PostgreSQL relational backends, a metrics TSDB,
  compressed log blocks, and an Inventory store — all green against a real
  PostgreSQL instance from day one (ADR-0003).
- Transport: HTTP/2 + Protobuf + zstd ingestion, with a local WAL buffer
  and backfill so a hub outage never loses data (ADR-0008).
- Privilege model (ADR-0005): short-lived, root, sandboxed helpers
  (`bitacora-smart` for S.M.A.R.T., `bitacora-vpn` for WireGuard/Tailscale,
  `bitacora-dnf` for `dnf check-update`) handing data to the unprivileged
  agent through a shared spool.
- Collectors: CPU, memory, journald, Docker (cgroup v2 + container names),
  public-surface exposure, SMB/NFS shares and their usage, system users,
  network traffic and VPN tunnels, UPS/SAI (NUT), hardware identity and
  CPU topology, per-disk storage breakdown, and pending
  package/plugin/image updates (apt, dnf, UnRaid plugins, Docker images).
- Regex-based extraction engine turning raw log lines into events
  (ADR-0006), an alerting engine with ntfy/webhook/Telegram/SMTP
  notifiers (ADR-0009).
- `bitacora-run` wrapper and the canonical `Job` model for backups, syncs
  and other scheduled operations (ADR-0010).
- In-memory 1 Hz black box, `pstore` crash diagnosis after an unclean
  boot, and segfault correlation against CPU fault-cluster topology
  (ADR-0011).
- Read-only hub API, a minimal web UI, native clients, a PWA, and QR-code
  device pairing (ADR-0014).
- CI pipeline: build/test, the `os/exec` allowlist check enforcing
  ADR-0012's read-only boundary, and secret scanning.

### Known gaps

- No `.deb`/`.rpm` packages yet — `packaging/` documents a manual install;
  real OS packaging is planned for a later release.
- No published container images yet, despite ADR documentation already
  naming `ghcr.io/bitacora-dev/` as the intended registry.
