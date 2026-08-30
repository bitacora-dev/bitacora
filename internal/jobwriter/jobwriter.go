// Package jobwriter hands a finished schema.Job off to the local agent
// (ADR-0010): over its Unix socket when reachable, or into the spool
// (ADR-0005's exchange directory, one file per job) when it isn't.
//
// bitacora-run must never fail — and must never block noticeably — just
// because the agent happens to be down. Everything here is a best-effort,
// short-timeout attempt with a fallback that always succeeds as long as
// the filesystem does.
package jobwriter

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

// DefaultSocketPath is where bitacora-agent listens for local writes, once
// it implements that side (not yet — see internal/jobwriter/README.md).
const DefaultSocketPath = "/run/bitacora/agent.sock"

// DefaultSpoolDir is the jobs subdirectory of the ADR-0005 spool
// (/var/lib/bitacora/spool), kept apart from the single-file-per-helper
// entries so an agent outage doesn't clobber one job's data with the next.
const DefaultSpoolDir = "/var/lib/bitacora/spool/jobs"

// dialTimeout bounds how long Write waits for the agent socket before
// giving up and falling back to the spool. It must stay short: a
// wrapper that hangs defeats the point of instrumenting cron jobs.
const dialTimeout = 500 * time.Millisecond

// Writer hands jobs off to the agent or the spool.
type Writer struct {
	SocketPath string
	SpoolDir   string
}

// New returns a Writer for the given socket path and spool directory.
func New(socketPath, spoolDir string) Writer {
	return Writer{SocketPath: socketPath, SpoolDir: spoolDir}
}

// Write delivers job to the local agent if it's reachable, or to the spool
// otherwise. usedSocket reports which path succeeded. err is only non-nil
// when both paths failed — the agent being unreachable is the expected,
// silent case ADR-0010 exists to handle, not a caller-visible error.
func (w Writer) Write(job schema.Job) (usedSocket bool, err error) {
	if err := w.writeSocket(job); err == nil {
		return true, nil
	}

	if err := spool.WriteAtomic(w.SpoolDir, job.ID, job.Schema, job, nil); err != nil {
		return false, fmt.Errorf("jobwriter: writing job %s to spool: %w", job.ID, err)
	}
	return false, nil
}

func (w Writer) writeSocket(job schema.Job) error {
	conn, err := net.DialTimeout("unix", w.SocketPath, dialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("jobwriter: marshaling job %s: %w", job.ID, err)
	}
	payload = append(payload, '\n')

	if err := conn.SetWriteDeadline(time.Now().Add(dialTimeout)); err != nil {
		return err
	}
	_, err = conn.Write(payload)
	return err
}
