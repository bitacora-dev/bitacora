# internal/extraction

Declarative YAML rules that promote a matched log line into an Event
(ADR-0006): logs are the raw material, events are what's been understood
from them.

- `rule.go` — `Rule`, `Parse`, `LoadDir`. A rule needs `id`, `match` (a Go
  regexp with named capture groups), and `emit.type`/`emit.severity`.
  `emit.enrich` names are validated against the built-in enrichers at load
  time — an unknown name is a load error, not a silent no-op.
- `engine.go` — `Engine.Process` matches a `schema.LogLine` against every
  rule (first match wins), runs any named enrichers, and builds the
  resulting `schema.Event`. `emit.title` is a `text/template` string
  rendered against the match's attrs (plus whatever enrichment added) —
  not part of ADR-0006's literal YAML shape, added because a rule needs a
  specific, readable title. `emit.fingerprint_fields` picks which attrs
  identify a *recurrence* of the same problem (defaults to every capture
  group when unset, which is coarser but a reasonable default for rules
  that don't need better).
- `enrich.go` — `cpu_from_context` (reads `/proc/<pid>/stat` if the
  process still exists) and `core_from_cpu` (maps that CPU to a physical
  core via `/sys/.../topology/core_id`). Both are best-effort: a
  process that's already gone (the normal case after a segfault) just
  means those attrs stay unset, never a failure.
- `rules/kernel-segfault.yaml` — the first rule, ADR-0006's own worked
  example.

## What `cpu_from_context` does NOT do yet

ADR-0006 also describes falling back to "el contexto del mensaje del
kernel" (nearby journal entries) when the process is already gone. A
single-line regex match doesn't have access to surrounding log lines —
that correlation is ADR-0011's stated differentiator (the black box:
segfault↔CPU-topology correlation from `pstore`/`netconsole` context), not
this package's job. Only the `/proc/<pid>/stat` branch is implemented.

## Testdata

`testdata/logs/kernel-segfault.log`, `testdata/proc/`, and `testdata/sys/`
are synthetic — realistic shapes, not captures from a real machine (none
was available while writing this). ADR-0006 asks for real, anonymized
captures; swap these when one exists.
