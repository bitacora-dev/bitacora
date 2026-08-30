# internal/collector/shareusage

Share disk-usage collector (ADR-0016): the `du -sh`-equivalent measurement
for each share `internal/collector/shares` reports as configured — how
much space it actually occupies, not just that it exists.

This is deliberately a separate collector from `shares`, registered at a
much longer interval (ADR-0016 calls for "una vez al día", wired here as
24h/1min in `cmd/bitacora-agent/main.go`): walking a multi-terabyte media
share can take minutes, which would be completely wrong for the same
5-minute cycle share *definitions* use.

- Reuses `shares.Paths()` (share ID → filesystem path) instead of
  duplicating smb.conf/exports parsing.
- `dirSize` sums every regular file's size under a share's root via
  `filepath.WalkDir`, checking `ctx` every 100 entries so a genuinely huge
  walk can still be cancelled. A missing root is an error (checked via an
  explicit `os.Lstat` before the walk starts); an unreadable file or
  subdirectory *within* an existing root is skipped, not fatal — one bad
  entry doesn't lose the rest of the measurement.
- One share failing to walk doesn't block the others: each share's size is
  measured independently, and a share can be silently absent from a given
  cycle's items if it fails.

`Requires()` returns nil for the same reason as `shares.Requires()`: it
needs "at least one of SMB/NFS", which the Registry's AND-only capability
check can't express, so it self-gates on whatever `shares.Paths()` finds.

## What's NOT here

- Per-file or per-subdirectory breakdown — this reports one total per
  share, not a full `du`-style tree.
- Any caching/incremental sizing — every cycle re-walks the whole share
  from scratch, which is exactly why the interval is so long.
