// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"testing"
	"time"
)

func validJob() Job {
	startedAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	return Job{
		ID:         "01J8XR000000000000000000",
		JobName:    "rclone-aginsur-sync",
		HostID:     "01J8X0000000000000000000",
		StartedAt:  startedAt,
		FinishedAt: startedAt.Add(time.Minute),
		Status:     JobSuccess,
		Schema:     CurrentSchemaVersion,
	}
}

func TestJob_Validate_LifecycleInvariants(t *testing.T) {
	tests := []struct {
		name string
		job  Job
		want bool
	}{
		{"running without finish", func() Job { j := validJob(); j.Status = JobRunning; j.FinishedAt = time.Time{}; return j }(), true},
		{"running with finish", func() Job { j := validJob(); j.Status = JobRunning; return j }(), false},
		{"terminal without finish", func() Job { j := validJob(); j.FinishedAt = time.Time{}; return j }(), false},
		{"terminal before start", func() Job { j := validJob(); j.FinishedAt = j.StartedAt.Add(-time.Second); return j }(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.job.Validate()
			if (err == nil) != tt.want {
				t.Fatalf("Validate() error = %v, want valid=%t", err, tt.want)
			}
		})
	}
}

func TestJob_Validate_Valid(t *testing.T) {
	if err := validJob().Validate(); err != nil {
		t.Fatalf("expected valid job, got error: %v", err)
	}
}

func TestJob_Validate_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		mut  func(j *Job)
	}{
		{"id", func(j *Job) { j.ID = "" }},
		{"job_name", func(j *Job) { j.JobName = "" }},
		{"host_id", func(j *Job) { j.HostID = "" }},
		{"started_at", func(j *Job) { j.StartedAt = time.Time{} }},
		{"status", func(j *Job) { j.Status = "bogus" }},
		{"schema", func(j *Job) { j.Schema = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := validJob()
			tt.mut(&j)
			if err := j.Validate(); err == nil {
				t.Fatalf("expected an error when %s is invalid", tt.name)
			}
		})
	}
}

func TestJobStatus_Valid(t *testing.T) {
	valid := []JobStatus{JobRunning, JobSuccess, JobWarning, JobFailed, JobTimeout, JobKilled}
	for _, s := range valid {
		if !s.valid() {
			t.Errorf("expected %q to be a valid status", s)
		}
	}
	if JobStatus("bogus").valid() {
		t.Error("expected bogus status to be invalid")
	}
}
