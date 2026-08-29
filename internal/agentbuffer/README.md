# internal/agentbuffer

The agent's local outbound WAL (ADR-0008): `/var/lib/bitacora/spool/outbound/`
in production. Metrics, events and log lines are appended durably before
the agent ever tries to send them, so a hub outage — even across an agent
or host restart — never loses what happened in the meantime.

- `buffer.go` — `Buffer.Open`/`Append`/`Close`. One active, uncompressed
  segment (fsynced on every write) rotates into a sealed `.wal.zst` file
  once it crosses `DefaultSegmentBytes` (4 MB). `Open` recovers both sealed
  segments and an orphaned active segment left by an unclean shutdown.
- `capacity.go` — `EnforceCapacity`: discards by priority (log lines, then
  raw metrics, **never events**), oldest first within a tier, until back
  within the 2h/256MB default budget. `BuildOverflowEvent` builds the
  `agent.buffer_overflow` event ADR-0008 says to emit — the caller decides
  how to deliver it (typically: `Append` it back into the same buffer).
- `backfill.go` — `Buffer.Backfill`: sends buffered items in chronological
  order, in batches, rate-limited between batches. An item is only removed
  (`Ack`) after its batch's send call succeeds — a failed send leaves
  everything from that point on still buffered, so retrying resumes
  instead of resending confirmed data.
- `transportsender.go` — `TransportSender` adapts a `transport.Client`
  into the `Sender` function `Backfill` needs, converting `Item`s into a
  `bitacorapb.Batch`. Reference wiring for the real agent; `Backfill`
  itself doesn't import `transport`.

## What's NOT here yet

- **Nothing calls `Append`/`EnforceCapacity`/`Backfill` from a running
  agent.** `cmd/bitacora-agent` doesn't have a collector run loop wired in
  yet (see followups on #646/#649) — this package is the buffer itself,
  ready to be driven once that loop exists.
- **Job priority.** ADR-0008 says "nunca eventos ni jobs" — there's no
  canonical `Job` schema type yet (that's ADR-0010), so only events are
  currently protected from discard. Add a `PriorityJob` tier (never
  discarded, same as events) once that type exists.
- **`bitacora_agent_buffer_bytes` / `bitacora_agent_last_flush_timestamp`**
  — the metrics ADR-0008 says the agent should expose about its own
  buffer. Straightforward once the metric-emission path exists, not built
  here.
