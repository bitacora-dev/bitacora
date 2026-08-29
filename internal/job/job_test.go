package job

import (
	"encoding/json"
	"testing"
	"time"
)

func sampleJob() Job {
	return Job{
		ID:              "01J8XR000000000000000000",
		JobName:         "rclone-aginsur-sync",
		HostID:          "01J8X0000000000000000000",
		StartedAt:       time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC),
		FinishedAt:      time.Date(2026, 8, 28, 2, 41, 33, 0, time.UTC),
		DurationSeconds: 2493,
		Status:          StatusSuccess,
		ExitCode:        0,
		Stats: Stats{
			"files_transferred": 1284,
			"bytes_transferred": 44230118400,
			"errors":            0,
		},
		Trigger: TriggerSystemd,
		Schema:  CurrentSchemaVersion,
	}
}

func TestJob_MarshalsWithADRFieldNames(t *testing.T) {
	encoded, err := json.Marshal(sampleJob())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, field := range []string{
		"id", "job_name", "host_id", "started_at", "finished_at",
		"duration_seconds", "status", "exit_code", "stats", "trigger", "schema",
	} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("expected JSON field %q, got keys %v", field, decoded)
		}
	}

	if decoded["duration_seconds"] != float64(2493) {
		t.Errorf("expected duration_seconds 2493, got %v", decoded["duration_seconds"])
	}
}

func TestJob_ValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(j *Job)
		wantErr bool
	}{
		{"valid job", func(j *Job) {}, false},
		{"missing id", func(j *Job) { j.ID = "" }, true},
		{"missing job_name", func(j *Job) { j.JobName = "" }, true},
		{"missing host_id", func(j *Job) { j.HostID = "" }, true},
		{"missing started_at", func(j *Job) { j.StartedAt = time.Time{} }, true},
		{"invalid status", func(j *Job) { j.Status = "bogus" }, true},
		{"missing schema", func(j *Job) { j.Schema = 0 }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := sampleJob()
			tc.mutate(&j)
			err := j.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestJob_ValidateAcceptsEveryStatus(t *testing.T) {
	for _, s := range []Status{StatusRunning, StatusSuccess, StatusWarning, StatusFailed, StatusTimeout, StatusKilled} {
		j := sampleJob()
		j.Status = s
		if err := j.Validate(); err != nil {
			t.Errorf("status %q: unexpected error: %v", s, err)
		}
	}
}
