# Bitácora

**Self-hosted observability and diagnostics for Linux servers.**

Metrics, events and logs on a single correlated timeline, so you can answer
"what happened at 01:05 last Tuesday?" without opening an SSH session.

## Why

Conventional monitoring tools sample every 10-30 seconds and rely on the
journal being flushed to disk. Neither survives a hard freeze. Bitácora keeps
a 1 Hz in-memory black box of the last 15 minutes, reads `pstore` after an
unclean boot, and correlates segfaults against CPU topology — so the incidents
that are currently impossible to investigate become a readable report.

## Status

Early design. No usable code yet.

Architecture decisions live in [`docs/adr/`](docs/adr/) (written in Spanish).

## Design principles

- **Read-only by design.** Bitácora never executes anything on the machines it
  observes. See [ADR-0012](docs/adr/0012-solo-lectura.md).
- **Never runs as root.** Privileged work is isolated in short-lived helpers.
- **No mandatory external services.** No Prometheus, no Loki, no cloud, no SaaS.
  A single binary and a data directory.
- **Low overhead.** Budget: ≤ 60 MB RSS and ≤ 2 % of one core per agent.

## License

- Core (`bitacora-hub`, `bitacora-agent`, `bita`, web UI): **AGPL-3.0**
- `bitacora-run`: **Apache-2.0**

See [ADR-0013](docs/adr/0013-nombre-licencia-y-gobernanza.md) for the rationale.
