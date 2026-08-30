# journald collector

Reads the systemd journal via `sdjournal`, with a persisted cursor so a
restart resumes instead of replaying or skipping (ADR-0005).

## A CGO exception, scoped and documented

`sdjournal` requires CGO and libsystemd — there's no pure-Go journal
reader. That's a deliberate exception to ADR-0001's general "no CGO in the
agent" rule, confirmed with the user in-session rather than decided
silently (the alternative, `journalctl -o json` via `os/exec`, would need
exec from the unprivileged agent process, which ADR-0012 reserves for
privileged helpers only).

Only `reader_linux.go` pays the CGO cost, behind a `//go:build linux` tag.
Everything else in this package — the `Collect` loop, cursor persistence,
journal-entry-to-`schema.LogLine` conversion — is plain Go, tested against
a fake `Reader`, and builds cgo-free on every platform including macOS.
**The real `sdjournal` path itself is verified in CI only** (ubuntu-latest,
with `libsystemd-dev` installed) — there was no Linux machine available to
run it against a real journal while writing this.

## Capability

`logs.journald`.

## Metrics

None — this collector only calls `Sink.LogLines`.

## Cursor persistence

Written atomically (temp file + fsync + rename) to `cursor_path`
(`/var/lib/bitacora/journald.cursor` by default) after every `Collect`
call that read at least one entry. A `Collect` call reads at most 500
entries (`maxEntriesPerCollect`) — a large backlog drains over several
ticks rather than blocking one call past the runtime's timeout (ADR-0007).
