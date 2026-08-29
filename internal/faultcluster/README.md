# internal/faultcluster

Segfault↔CPU-topology correlation (ADR-0011) — the project's stated
differentiator: turning "hay segfaults sueltos" into "34 segfaults en 6
días, 31 de ellos en el core físico 4; la probabilidad de que sea azar es
del 0,0001%", automating exactly the reasoning that diagnosed the
incident this ADR is named after.

- `topology.go` — `ReadTopology`: maps logical CPUs to physical cores
  (`/sys/.../topology/core_id`) and online state, plus best-effort
  P-core/E-core classification via the hybrid-CPU PMU device lists at
  `/sys/bus/event_source/devices/{cpu_core,cpu_atom}/cpus` (Intel hybrid
  CPUs only — everyone else gets `CoreTypeUnknown`, not an error).
- `binomial.go` — `upperTailPValue`: the one-sided binomial test itself,
  via the standard successive-ratio recurrence rather than raw
  coefficients, numerically stable without arbitrary-precision math for
  any realistic sample size.
- `tracker.go` — `Tracker.Observe`: counts segfaults per physical core
  and emits one `hw.cpu_fault_cluster` Event the first time a core's
  share becomes implausible under "uniform across active cores"
  (ADR-0011: `p < 0.01`, at least `MinSamples` (5) faults on that core).
  Flags each core **once** — re-alerting on an already-identified bad
  core is alerting's job (ADR-0009), driven off this event, not this
  package repeating itself.

## Where the segfault extraction and CPU enrichment come from

This package correlates; it doesn't parse journal lines or read
`/proc/<pid>/stat` itself. That's already built, from ADR-0006's own
worked example:

- `internal/extraction/rules/kernel-segfault.yaml` matches
  `comm[pid]: segfault at ADDR ip IP sp SP error ERR` and enriches it
  with `cpu` (via the `cpu_from_context` enricher, `/proc/<pid>/stat`'s
  processor field) and `core_id` (via `core_from_cpu`,
  `/sys/.../topology/core_id`).
- That rule only ever matched a real journal entry once this task fixed
  `internal/collector/journald`'s `Source` field to reflect the journal's
  own `_TRANSPORT` value instead of always hardcoding `"journald"` — see
  that package's changelog. Without the fix, every journal line looked
  like `source: journald` to the extraction engine, and the rule's
  `source: kernel` filter could never match, kernel messages included.

`Tracker.Observe` takes the logical CPU straight from that already-parsed
`cpu` attribute — the correlation layer's whole job is the statistics on
top, not re-deriving what the extraction engine already produced.

## What's NOT here

- **Wiring a `Tracker` into a running agent or hub.** Nothing currently
  feeds `extraction.Engine`'s output into `Tracker.Observe` — this
  package is a tested library, same situation as every other "not wired
  into the runtime yet" component from this project's earlier tasks.
- **Offline-CPU-change events.** `Topology.OfflineCPUs` exists and is
  tested, but nothing currently diffs two `Topology` snapshots over time
  to emit "CPU N went offline at T" — ADR-0011 wants this "para que la
  prueba diagnóstica en curso quede documentada en la línea temporal",
  and it's a natural, small addition on top of what's here, just not
  built yet.
- **A resolved `core_type` for segfault events themselves.**
  `kernel-segfault.yaml`'s `core_from_cpu` enricher sets `core_id` but not
  P-core/E-core — `Tracker`'s own event does carry `core_type` (read
  from its `Topology`), but the underlying `kernel.segfault` Event does
  not. Not needed for the correlation itself, worth adding if the
  timeline UI wants to show it per-segfault too.
