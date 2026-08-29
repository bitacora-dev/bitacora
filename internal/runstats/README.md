# internal/runstats

Extractors that turn a wrapped command's captured output into canonical
`schema.JobStats` (ADR-0010). Used by `bitacora-run` (`cmd/bitacora-run`)
right after a command exits — never anywhere the command itself runs, so
they stay testable with nothing but a `testdata/` fixture.

- `runstats.go` — the `Extractor` interface and `For(argv)`, which picks
  the extractor matching the wrapped command, falling back to `Generic`.
- `generic.go` — the fallback for anything with no dedicated extractor:
  stdout/stderr line counts and a 1 on non-zero exit.
- `rclone/` — parses `--use-json-log` output; no regex, just the last
  `stats` object logged.
- `rsync/` — parses the `--stats` summary block and counts `rsync: ` /
  `rsync error: `-prefixed stderr lines.
- `snapraid/` — parses the `sync`/`scrub` "`<count> <label>`" summary
  block that precedes "Everything OK".

Each extractor is detected by `argv[0]`'s basename, so `bitacora-run --job
X -- rclone sync ...` and `bitacora-run --job X -- /usr/bin/rclone sync
...` both match. A parsing failure is a non-fatal error appended to the
job's log, never a reason to fail the wrapped command — it already ran and
already has its real exit code by the time an extractor sees anything.

Extractors added for `tar`, `restic`, `borg` and further tools go here
following the same shape (ADR-0010's table of extractors by command).
