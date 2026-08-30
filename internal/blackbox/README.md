# internal/blackbox

The high-frequency recorder (ADR-0011): a preallocated ring buffer,
sampled at 1 Hz, memory-mapped and flushed to disk every 5 s — the one
data source fine-grained enough to have caught the incident that
motivates this ADR, since a hard hang loses exactly the last seconds a
journal-based system would need.

- `sample.go` — `Sample`, a fixed-size struct (no strings, no slices,
  capped arrays) so it encodes to a constant number of bytes and Record
  never allocates on its hot path.
- `header.go` / `encode.go` — the on-disk format: a 64-byte header
  (magic, version, capacity, write index) followed by fixed-size records.
- `recorder.go` — `Recorder`: `Record` writes into the mmap'd region
  (fast, in-page-cache); `Sync` msyncs it to disk (ADR-0011's "volcado
  cada 5 s"). Reopening an existing file resumes its write index instead
  of resetting the ring, so an agent restart doesn't lose the pre-fail
  window.
- `reader.go` — `Dump`: reads a file with a plain `os.ReadFile`, no mmap,
  no agent — ADR-0011: "debe ser legible [...] sobre un fichero copiado
  desde otra máquina." Wired to `bita blackbox dump <fichero>`.
- `sampler.go` + `sampler_*.go` — `Sampler`: gathers one `Sample` from
  `/proc` and `/sys`, best-effort per metric group (a host without EDAC,
  hwmon, or a hybrid CPU just gets zero values there, not a failure).
  Deliberately reimplements CPU-busy-ratio math instead of importing
  `internal/collector/cpu`: ADR-0011 requires "camino de código separado
  del runtime de collectors [...] debe sobrevivir a un agente degradado",
  and importing the collector package would undercut that independence.
- `run.go` — `Run`: the 1 Hz sample / 5 s sync loop, with its own
  injectable `Clock`/`Ticker` (redefined here rather than imported from
  `internal/collector`, same independence reasoning as above).

Mandatory test from ADR-0011's own implementation notes — "inyectar
SIGKILL -9 al agente y verificar que el fichero mapeado contiene datos
coherentes hasta el último volcado" — is `sigkill_test.go`'s
`TestRecorder_SurvivesSIGKILL`: a real child process records into a real
mmap'd file and gets SIGKILL'd mid-flight; a separate process (this test)
reads the file back afterward.

## What's NOT here

- **Wiring `Run` into `cmd/bitacora-agent`.** Nothing in the agent
  binary starts a `Recorder`/`Sampler`/`Run` yet — this package is built
  and tested standalone, same situation as `agentbuffer.Backfill` and
  `job.Server` before it.
- **Ingesting the recovered pre-fail window as high-resolution metrics
  after an unclean shutdown.** ADR-0011: "al arrancar tras un reinicio no
  limpio, el agente [...] la marca como *ventana pre-fallo*" — that's
  agent startup logic that doesn't exist yet, on top of unclean-shutdown
  detection itself.
- **The resource budget itself isn't asserted by a test.** ADR-0011
  targets "< 0,5 % de un core y < 10 MB"; the design achieves that
  structurally (fixed-size arrays, no allocation on the hot path, no
  reflection at runtime), but CPU%/RSS aren't something a unit test can
  reliably assert in CI — this is a documented gap, not a silently
  skipped requirement.
- **CPU frequency for cores without `cpufreq`** (e.g. inside some VMs) —
  `CPUFreqMHz` is simply zero there, same best-effort posture as every
  other optional metric group.
