# internal/job

The canonical `Job` model (ADR-0010): one representation for anything
periodic — backups, syncs, scrubs, updates — regardless of which tool
produced it, plus the local delivery path `cmd/bitacora-run` uses to get a
Job to the agent.

- `job.go` — the `Job` struct, matching ADR-0010's schema field-for-field,
  plus `Signal` (which run of the job, if any, killed it) and
  `OutputLines`, both real fields the ADR's prose requires but its JSON
  example doesn't spell out.
- `spool.go` — the durable local fallback: one JSON file per job, named by
  ID, written atomically (temp file, fsync, rename). Deliberately its own
  contract, not a reuse of `internal/spool`'s: that package is one
  overwritten file per collector name, built for helpers that each own a
  single entry — Jobs need many files coexisting instead.
- `socket.go` — `Deliver`/`Server`: a Unix-socket, newline-delimited-JSON
  protocol for handing one Job to a locally running agent and getting back
  `ok` or an error.
- `deliver.go` — `Report` (socket first, spool fallback — ADR-0010: "al
  agente local [...], o al spool si el agente no está disponible") and
  `Backfill` (drains the spool into the agent once it's reachable again,
  oldest first, stopping at the first failure so a retry resumes instead of
  resending — same contract as `agentbuffer.Backfill`).
- `extract/` — the per-command-type stats extractors (`rclone`, `rsync`,
  `snapraid`, and a `generic` fallback that always succeeds).

## What's NOT here

- **A `Server` actually running inside `cmd/bitacora-agent`.** The Unix
  socket protocol and the `Receiver` interface are built and tested against
  a real listener, but nothing wires them into the agent daemon yet — same
  situation as `agentbuffer.Backfill`, which exists and is tested but isn't
  called from `main.go` either. A real agent needs to accept `Job`s,
  forward them to the hub (ADR-0008's transport, extended with a Job
  message type — not part of this task), and periodically call
  `job.Backfill` against its own spool directory on startup and
  reconnection.
- **Reliable automatic cron detection.** `bitacora-run` detects `systemd`
  via `$INVOCATION_ID`, a real mechanism systemd sets for every unit it
  runs. Cron has no equivalent: it doesn't mark its children in any stable,
  documented way. Rather than guess (e.g. from TTY absence, which also
  misclassifies test/CI runs), `bitacora-run` defaults to `manual` and
  expects `--trigger cron` in the crontab entry itself.
- **`peer_host_id`, `next_expected`, and the deadman-rule proposal.**
  ADR-0010's "Descubrimiento y deadman automático" section — inferring a
  job's expected cadence, proposing a deadman rule after three regular
  runs, reading systemd `OnCalendar` schedules — is hub/agent-side logic
  that doesn't exist yet, not something `bitacora-run` itself can know.
- **The historical-log-import collector.** ADR-0010 also describes a
  "camino de compatibilidad" collector that re-applies these same
  extractors to pre-existing log files
  (`/var/log/icloudserver/rsync/...`). Explicitly the non-primary path per
  the ADR's own text, and out of this task's scope.
- **The SnapRAID extractor's precision.** Unlike rclone (structured JSON)
  and rsync (a stable, well-known `--stats` block), SnapRAID has no
  machine-readable output mode and this extractor's confidence in its exact
  wording is lower — see the comment in `extract/snapraid.go`. It's
  fixture-tested against a representative transcript, not against a real
  SnapRAID array.
