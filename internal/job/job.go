// Package job defines the canonical Job model (ADR-0010): the single
// representation for anything periodic — backups, syncs, scrubs, updates —
// regardless of which tool produced it.
package job

import (
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// CurrentSchemaVersion is the schema version stamped on new Jobs.
const CurrentSchemaVersion = 1

// Status is a Job's lifecycle state, per ADR-0010.
type Status string

// Valid Status values.
const (
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusWarning Status = "warning"
	StatusFailed  Status = "failed"
	StatusTimeout Status = "timeout"
	StatusKilled  Status = "killed"
)

func (s Status) valid() bool {
	switch s {
	case StatusRunning, StatusSuccess, StatusWarning, StatusFailed, StatusTimeout, StatusKilled:
		return true
	default:
		return false
	}
}

// Trigger values bitacora-run can attribute a run to. Only Systemd is ever
// auto-detected with certainty (via $INVOCATION_ID); Cron and Manual are
// either passed explicitly with --trigger or default to Manual — see
// cmd/bitacora-run's README for why reliable automatic cron detection isn't
// attempted.
const (
	TriggerSystemd = "systemd-timer"
	TriggerCron    = "cron"
	TriggerManual  = "manual"
)

// Stats holds tool-specific statistics: canonical keys when the extractor
// that ran knows them (files_transferred, bytes_transferred, ...), free-form
// otherwise (ADR-0010: "un mapa con claves canónicas cuando existen y libres
// cuando no").
type Stats map[string]any

// Job is the canonical record for one run of anything periodic, per
// ADR-0010's schema.
type Job struct {
	ID              string    `json:"id"`
	JobName         string    `json:"job_name"`
	HostID          string    `json:"host_id"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	DurationSeconds float64   `json:"duration_seconds,omitempty"`
	Status          Status    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	// Signal is the signal that ended the process, when Status is Killed or
	// Timeout (e.g. "SIGKILL"). Not part of ADR-0010's literal JSON example,
	// but required by its own text: "queda registrado como killed, con su
	// señal" — there has to be a field to hold that señal.
	Signal string `json:"signal,omitempty"`
	// OutputLines is how many combined stdout+stderr lines the command
	// produced. Always set, regardless of which extractor ran — it's the
	// "genérico" column from ADR-0010's extractor table.
	OutputLines  int             `json:"output_lines,omitempty"`
	Stats        Stats           `json:"stats,omitempty"`
	PeerHostID   string          `json:"peer_host_id,omitempty"`
	Trigger      string          `json:"trigger,omitempty"`
	NextExpected time.Time       `json:"next_expected,omitempty"`
	LogRefs      []schema.LogRef `json:"log_refs,omitempty"`
	Schema       int             `json:"schema"`
}

// Validate enforces the required fields.
func (j Job) Validate() error {
	if j.ID == "" {
		return fmt.Errorf("job: id is required")
	}
	if j.JobName == "" {
		return fmt.Errorf("job %q: job_name is required", j.ID)
	}
	if j.HostID == "" {
		return fmt.Errorf("job %q: host_id is required", j.ID)
	}
	if j.StartedAt.IsZero() {
		return fmt.Errorf("job %q: started_at is required", j.ID)
	}
	if !j.Status.valid() {
		return fmt.Errorf("job %q: invalid status %q", j.ID, j.Status)
	}
	if j.Schema < 1 {
		return fmt.Errorf("job %q: schema must be >= 1", j.ID)
	}
	return nil
}
