package schema

import (
	"testing"
	"time"
)

func validJob() Job {
	return Job{
		ID:        "01J8XR000000000000000000",
		JobName:   "rclone-aginsur-sync",
		HostID:    "01J8X0000000000000000000",
		StartedAt: time.Now().UTC(),
		Status:    JobSuccess,
		Schema:    CurrentSchemaVersion,
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
