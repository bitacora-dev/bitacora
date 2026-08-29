# internal/jobwriter

Hands a finished `schema.Job` off to the local agent (ADR-0010): a
newline-delimited JSON object over the agent's Unix socket
(`DefaultSocketPath`, `/run/bitacora/agent.sock`) when reachable, or into
the ADR-0005 spool (`DefaultSpoolDir`,
`/var/lib/bitacora/spool/jobs/<job_id>.json`, one file per job) when it
isn't.

`Write` never treats an unreachable socket as an error — that's the
expected path whenever the agent is stopped or hasn't started yet, and
it's exactly the case ADR-0010 calls out as mandatory to handle without
losing the job.

## What's NOT here yet

`bitacora-agent` doesn't listen on `DefaultSocketPath` yet — there's no
code anywhere that reads what this package writes to the socket path,
only the spool path is currently exercised end-to-end. Wiring the
agent-side listener (accept the connection, decode the JSON line, hand it
to the agent's own ingestion path, read `/var/lib/bitacora/spool/jobs/`
alongside the other spool entries) is a separate task on the agent side;
this package's contract (line-delimited JSON `schema.Job`, one per
connection) is designed to be simple enough that binding it needs no
Go dependency beyond `encoding/json` and `net`, matching bitacora-run's
"no dependencies, millisecond startup" requirement (ADR-0010).
