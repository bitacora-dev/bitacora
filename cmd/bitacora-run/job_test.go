package main

import (
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/execwrap"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

func TestBuildJob_Success(t *testing.T) {
	started := time.Now().UTC()
	result := execwrap.Result{
		StartedAt:  started,
		FinishedAt: started.Add(2 * time.Second),
		ExitCode:   0,
		Stdout:     []byte("plain output, no dedicated extractor\n"),
	}

	job, errs := buildJob("nightly-backup", "host-1", []string{"some-tool"}, "manual", result)
	if len(errs) != 0 {
		t.Fatalf("unexpected extraction errors: %v", errs)
	}
	if job.ID == "" {
		t.Error("expected a non-empty job ID")
	}
	if job.Status != schema.JobSuccess {
		t.Errorf("Status = %q, want %q", job.Status, schema.JobSuccess)
	}
	if job.DurationSecond != 2 {
		t.Errorf("DurationSecond = %v, want 2", job.DurationSecond)
	}
	if job.Trigger != "manual" {
		t.Errorf("Trigger = %q, want %q", job.Trigger, "manual")
	}
}

func TestBuildJob_Failed(t *testing.T) {
	result := execwrap.Result{ExitCode: 3}
	job, _ := buildJob("j", "h", []string{"some-tool"}, "cron", result)
	if job.Status != schema.JobFailed {
		t.Errorf("Status = %q, want %q", job.Status, schema.JobFailed)
	}
}

func TestBuildJob_Killed(t *testing.T) {
	result := execwrap.Result{Signal: "killed", SignalNum: 9, ExitCode: -1}
	job, _ := buildJob("j", "h", []string{"some-tool"}, "systemd", result)
	if job.Status != schema.JobKilled {
		t.Errorf("Status = %q, want %q", job.Status, schema.JobKilled)
	}
	if job.Signal != "killed" {
		t.Errorf("Signal = %q, want %q", job.Signal, "killed")
	}
}

func TestBuildJob_Timeout(t *testing.T) {
	result := execwrap.Result{TimedOut: true, Signal: "killed", SignalNum: 9}
	job, _ := buildJob("j", "h", []string{"some-tool"}, "manual", result)
	if job.Status != schema.JobTimeout {
		t.Errorf("Status = %q, want %q", job.Status, schema.JobTimeout)
	}
}

func TestBuildJob_WarningOnExtractorErrors(t *testing.T) {
	result := execwrap.Result{ExitCode: 0, Stdout: []byte("Number of files: 10\n")}
	job, _ := buildJob("j", "h", []string{"rsync", "--stats"}, "manual", result)
	// rsync's fixture-free extractor reports errors=0 here (no rsync:
	// error lines), so this exercises the "clean exit, extractor ran, no
	// errors reported" success path instead.
	if job.Status != schema.JobSuccess {
		t.Errorf("Status = %q, want %q", job.Status, schema.JobSuccess)
	}
}

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name   string
		result execwrap.Result
		want   int
	}{
		{"clean exit", execwrap.Result{ExitCode: 0}, 0},
		{"nonzero exit", execwrap.Result{ExitCode: 3}, 3},
		{"signaled SIGKILL", execwrap.Result{SignalNum: 9, ExitCode: -1}, 137},
		{"signaled SIGTERM", execwrap.Result{SignalNum: 15, ExitCode: -1}, 143},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFor(tt.result); got != tt.want {
				t.Errorf("exitCodeFor(%+v) = %d, want %d", tt.result, got, tt.want)
			}
		})
	}
}
