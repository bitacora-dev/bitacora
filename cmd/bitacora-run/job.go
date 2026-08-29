package main

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/execwrap"
	"github.com/bitacora-dev/bitacora/internal/runstats"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// buildJob turns an execwrap.Result into the canonical schema.Job
// (ADR-0010), running the matching runstats extractor over the captured
// output. Extraction errors are returned alongside the job, not as a
// failure — a stats-parsing problem must never stop the job from being
// recorded, since by this point the wrapped command has already run to
// completion.
func buildJob(jobName, hostID string, argv []string, trigger string, result execwrap.Result) (schema.Job, []string) {
	stats, extractErrs := runstats.For(argv).Extract(result.Stdout, result.Stderr, result.ExitCode)

	job := schema.Job{
		ID:             newULID(result.StartedAt),
		JobName:        jobName,
		HostID:         hostID,
		StartedAt:      result.StartedAt,
		FinishedAt:     result.FinishedAt,
		DurationSecond: result.FinishedAt.Sub(result.StartedAt).Seconds(),
		Status:         statusFor(result, stats),
		ExitCode:       result.ExitCode,
		Signal:         result.Signal,
		Stats:          stats,
		Trigger:        trigger,
		Schema:         schema.CurrentSchemaVersion,
	}
	return job, extractErrs
}

// statusFor maps how the command ended to a JobStatus (ADR-0010): a
// timeout and an external kill are distinct from an ordinary non-zero
// exit, and a clean exit with extractor-reported errors is a warning, not
// an unqualified success.
func statusFor(result execwrap.Result, stats schema.JobStats) schema.JobStatus {
	switch {
	case result.TimedOut:
		return schema.JobTimeout
	case result.Signal != "":
		return schema.JobKilled
	case result.ExitCode != 0:
		return schema.JobFailed
	case statErrCount(stats) > 0:
		return schema.JobWarning
	default:
		return schema.JobSuccess
	}
}

func statErrCount(stats schema.JobStats) int64 {
	switch n := stats[schema.StatErrors].(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// exitCodeFor is the code bitacora-run itself exits with, propagating the
// wrapped command's outcome unaltered (ADR-0010). A signaled process has
// no usable ExitCode from the OS, so this follows the standard shell
// convention (128 + signal number) instead of exposing exec.ExitError's
// internal -1.
func exitCodeFor(result execwrap.Result) int {
	if result.SignalNum > 0 {
		return 128 + result.SignalNum
	}
	return result.ExitCode
}

// newULID generates a job ID, falling back to an RFC3339-ish timestamp
// (still unique enough to be useful) if entropy generation ever fails —
// a Job must always get an ID, since nothing downstream can identify one
// without it.
func newULID(t time.Time) string {
	entropy := ulid.Monotonic(rand.Reader, 0)
	id, err := ulid.New(ulid.Timestamp(t), entropy)
	if err != nil {
		return t.Format("20060102T150405.000000000Z07:00")
	}
	return id.String()
}
