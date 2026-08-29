# internal/alerting

The hub's alert state machine (ADR-0009): `inactive → pending → firing →
resolved`, with `for`, hysteresis, deduplication, silences, and deadman
(absence) detection.

- `alert.go` — `Alert.Evaluate`/`EvaluateFor`: the state machine itself.
  Returns true only on a *fresh* firing or resolved transition — repeated
  evaluations while already firing return false, which is
  ADR-0009's deduplication falling directly out of the state machine.
  Every transition is recorded in `Alert.History`.
- `hysteresis.go` — `HysteresisThreshold.ConditionTrue`: separate
  fire/resolve thresholds so a value oscillating at the boundary doesn't
  flap. Feed its output into `Alert.Evaluate` as `conditionTrue`.
- `silence.go` — `Silence`/`SilenceStore`. `NewSilence` refuses a missing
  or backwards end time — ADR-0009: "un silencio sin fecha de fin no se
  permite."
- `manager.go` — `Manager.Evaluate` ties fingerprint-based dedup (same
  rule + labels always resolves to the same `Alert`) and silence
  suppression together: a silenced alert still transitions and records
  history, it just doesn't return `shouldNotify`.
- `deadman.go` — `DeadmanTracker`: "esperaba una ejecución cada 24h y
  llevo 31h sin verla." Applies to job runs, agent reports, helpers
  writing to the spool, collectors producing samples — anything with an
  expected cadence.
- `externaldeadman.go` — `ExternalDeadman`: the hub pings an external
  service (ntfy, or a healthchecks.io-style endpoint on the VPS) so a
  hung hub is visible from outside iCloudServer.

## What this package deliberately does NOT do

- **Evaluate rule conditions against metrics or events.** Threshold
  expressions (`bitacora_cpu_temperature_celsius > 85`) and event-match
  counting (`on_event: kernel.segfault, threshold: {count: 3, window:
  24h}`) are rule-type-specific logic that belongs to whatever wires this
  to `metricstore`/`storage`/`extraction` — not built here. This package
  only owns what ADR-0009 calls out as the hard part: the state machine
  and its guarantees, independent of what's driving it.
- **Notifiers** (ntfy, webhook, Telegram, SMTP) — separate task (#661).
  `Manager.Evaluate`'s `shouldNotify` return value is exactly what a
  notifier dispatch loop would consume.
- **Grouping**, rate limiting per channel, `bita rules test`, and the
  Task Queue AI webhook payload — all explicitly out of this task's scope
  per ADR-0009's own notes.
