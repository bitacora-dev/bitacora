package schema

import (
	"fmt"
	"time"
)

// JobStatus is a Job's terminal (or in-progress) state (ADR-0010).
type JobStatus string

// Valid JobStatus values, per ADR-0010.
const (
	JobRunning JobStatus = "running"
	JobSuccess JobStatus = "success"
	JobWarning JobStatus = "warning"
	JobFailed  JobStatus = "failed"
	JobTimeout JobStatus = "timeout"
	JobKilled  JobStatus = "killed"
)

func (s JobStatus) valid() bool {
	switch s {
	case JobRunning, JobSuccess, JobWarning, JobFailed, JobTimeout, JobKilled:
		return true
	default:
		return false
	}
}

// JobStats holds the extractor-populated statistics for a Job. Keys are
// canonical when an extractor recognizes them (files_transferred,
// bytes_transferred, files_deleted, files_checked, errors) and free-form
// otherwise (ADR-0010: "stats es un mapa con claves canónicas cuando existen
// y libres cuando no").
type JobStats map[string]any

// Canonical JobStats keys, populated by extractors when the underlying tool
// reports them.
const (
	StatFilesTransferred = "files_transferred"
	StatBytesTransferred = "bytes_transferred"
	StatFilesDeleted     = "files_deleted"
	StatFilesChecked     = "files_checked"
	StatErrors           = "errors"
)

// JobLogRef anchors a Job to the range of log lines its execution produced.
type JobLogRef struct {
	BlockID string `json:"block_id"`
	From    int    `json:"from"`
	To      int    `json:"to"`
}

// Job is the canonical model for anything periodic: backups, syncs, scrubs,
// updates (ADR-0010). One model, one view, one alerting path for all of it.
type Job struct {
	ID             string      `json:"id"`
	JobName        string      `json:"job_name"`
	HostID         string      `json:"host_id"`
	StartedAt      time.Time   `json:"started_at"`
	FinishedAt     time.Time   `json:"finished_at,omitempty"`
	DurationSecond float64     `json:"duration_seconds,omitempty"`
	Status         JobStatus   `json:"status"`
	ExitCode       int         `json:"exit_code"`
	Signal         string      `json:"signal,omitempty"`
	Stats          JobStats    `json:"stats,omitempty"`
	PeerHostID     string      `json:"peer_host_id,omitempty"`
	Trigger        string      `json:"trigger,omitempty"`
	NextExpected   time.Time   `json:"next_expected,omitempty"`
	LogRefs        []JobLogRef `json:"log_refs,omitempty"`
	Schema         int         `json:"schema"`
}

// Validate enforces the ADR-0010 required fields and conventions.
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
