# internal/schema

Canonical data model shared by every piece of Bitácora (ADR-0006): `Metric`,
`Event`, `LogLine`, and the `host_id` that ties them together (ADR-0004).
Nothing that stores, transports or displays data should invent its own
shape — collectors, storage and transport all use these types.

- `metric.go` — Prometheus-model metric with `Validate()`: enforces the
  `bitacora_` prefix, the allowed unit suffixes, the `_ratio` 0-1 bound, and
  the forbidden-label rules (no `pid`, no untruncated `container_id`, no
  file paths or IP addresses as label values).
- `cardinality.go` — `CardinalityTracker`, enforcing the
  `MaxActiveSeriesPerHost` (2000) budget across every series seen for a
  host. Callers wire this into their own integration tests to fail CI when a
  collector's label combinations blow the budget.
- `event.go` — the discrete-fact type with `fingerprint`, `attrs`,
  `log_refs`.
- `logline.go` — raw log line, promoted to an `Event` only by an extraction
  rule.
- `job.go` — the canonical model for anything periodic: backups, syncs,
  scrubs, updates (ADR-0010). Produced primarily by `bitacora-run`
  (`cmd/bitacora-run`), with `stats` populated by `internal/runstats`
  extractors.
- `hostid.go` — `LoadOrCreateHostID`, a ULID generated once and persisted at
  `/var/lib/bitacora/host_id`, never derived from hostname, MAC or
  `machine-id`.
