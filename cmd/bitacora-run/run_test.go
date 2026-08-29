package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/job"
)

type captureReceiver struct {
	received []job.Job
}

func (r *captureReceiver) ReceiveJob(ctx context.Context, j job.Job) error {
	r.received = append(r.received, j)
	return nil
}

func testOptions(t *testing.T) Options {
	t.Helper()
	dir, err := os.MkdirTemp("", "bjrun")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	return Options{
		JobName:     "test-job",
		GracePeriod: 300 * time.Millisecond,
		SocketPath:  filepath.Join(dir, "no-agent.sock"), // nothing listens here
		SpoolDir:    filepath.Join(dir, "spool"),
		HostIDPath:  filepath.Join(dir, "host_id"),
	}
}

// TestExecute_ChildKilledBySignal_RecordsKilledWithSignal is ADR-0010's
// first mandatory test: "matar el proceso hijo con SIGKILL a mitad y
// verificar que el job queda registrado como killed con la señal
// correcta." The child kills itself, simulating an external kill (e.g.
// OOM) that bitacora-run never initiated — distinct from the --timeout
// path, which also results in a signaled death but is classified
// "timeout" instead (see TestExecute_TimeoutEscalatesToSIGKILL).
func TestExecute_ChildKilledBySignal_RecordsKilledWithSignal(t *testing.T) {
	opts := testOptions(t)
	opts.Command = "sh"
	opts.Args = []string{"-c", "kill -9 $$"}

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	if result.Job.Status != job.StatusKilled {
		t.Fatalf("expected status killed, got %q", result.Job.Status)
	}
	if result.Job.Signal != "SIGKILL" {
		t.Fatalf("expected signal SIGKILL, got %q", result.Job.Signal)
	}
}

// TestExecute_AgentDown_RunsAnywayAndJobAppearsWhenAgentReturns is
// ADR-0010's second mandatory test: "con el agente parado, el comando se
// ejecuta igual y el job aparece cuando el agente vuelve."
func TestExecute_AgentDown_RunsAnywayAndJobAppearsWhenAgentReturns(t *testing.T) {
	opts := testOptions(t)

	marker := filepath.Join(t.TempDir(), "ran")
	opts.Command = "sh"
	opts.Args = []string{"-c", "touch " + marker}

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	// The command ran, regardless of the agent being unreachable.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected the wrapped command to have run despite the agent being down: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Job.Status != job.StatusSuccess {
		t.Fatalf("expected status success, got %q", result.Job.Status)
	}

	// The job appears — in the spool, since the agent was down.
	spooled, err := job.ReadSpool(opts.SpoolDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 1 || spooled[0].Job.ID != result.Job.ID {
		t.Fatalf("expected the job to be spooled, got %+v", spooled)
	}

	// The agent comes back: backfilling delivers the spooled job and
	// drains it, exactly like a real agent restart would trigger.
	ln := listenUnix(t, opts.SocketPath)
	receiver := &captureReceiver{}
	srv := &job.Server{Receiver: receiver}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, ln)

	sent, err := job.Backfill(context.Background(), opts.SocketPath, opts.SpoolDir, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent != 1 {
		t.Fatalf("expected 1 job backfilled, got %d", sent)
	}
	if len(receiver.received) != 1 || receiver.received[0].ID != result.Job.ID {
		t.Fatalf("expected the agent to have received the job on backfill, got %+v", receiver.received)
	}
}

func TestExecute_TimeoutEscalatesToSIGKILL(t *testing.T) {
	opts := testOptions(t)
	opts.Timeout = 150 * time.Millisecond
	opts.GracePeriod = 200 * time.Millisecond
	opts.Command = "sh"
	opts.Args = []string{"-c", "trap '' TERM; sleep 5"}

	start := time.Now()
	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})
	elapsed := time.Since(start)

	if result.Job.Status != job.StatusTimeout {
		t.Fatalf("expected status timeout, got %q", result.Job.Status)
	}
	if result.Job.Signal != "SIGKILL" {
		t.Fatalf("expected SIGKILL after the child ignored SIGTERM, got %q", result.Job.Signal)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("expected the wrapper to have escalated well before sleep 5 finished, took %v", elapsed)
	}
}

func TestExecute_TimeoutRespectedBySIGTERM(t *testing.T) {
	opts := testOptions(t)
	opts.Timeout = 150 * time.Millisecond
	opts.Command = "sleep"
	opts.Args = []string{"5"}

	start := time.Now()
	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})
	elapsed := time.Since(start)

	if result.Job.Status != job.StatusTimeout {
		t.Fatalf("expected status timeout, got %q", result.Job.Status)
	}
	if result.Job.Signal != "SIGTERM" {
		t.Fatalf("expected SIGTERM (sleep doesn't trap it), got %q", result.Job.Signal)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected sleep to have died from SIGTERM well before 5s, took %v", elapsed)
	}
}

func TestExecute_SuccessfulRun(t *testing.T) {
	opts := testOptions(t)
	opts.Command = "true"

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	if result.ExitCode != 0 || result.Job.Status != job.StatusSuccess || result.Job.ExitCode != 0 {
		t.Fatalf("expected a clean success, got %+v", result)
	}
}

func TestExecute_NonZeroExit_RecordsFailedAndRealExitCode(t *testing.T) {
	opts := testOptions(t)
	opts.Command = "sh"
	opts.Args = []string{"-c", "exit 3"}

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	if result.ExitCode != 3 {
		t.Fatalf("expected exit code 3, got %d", result.ExitCode)
	}
	if result.Job.Status != job.StatusFailed || result.Job.ExitCode != 3 {
		t.Fatalf("expected failed/3, got %+v", result.Job)
	}
}

func TestExecute_CommandNotFound_StillReportsAndExits127(t *testing.T) {
	opts := testOptions(t)
	opts.Command = "this-binary-does-not-exist-anywhere"

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	if result.ExitCode != 127 {
		t.Fatalf("expected exit code 127, got %d", result.ExitCode)
	}
	if result.Job.Status != job.StatusFailed {
		t.Fatalf("expected status failed, got %q", result.Job.Status)
	}
}

func TestExecute_PassesThroughStdoutAndStderrLive(t *testing.T) {
	opts := testOptions(t)
	opts.Command = "sh"
	opts.Args = []string{"-c", "echo out-line; echo err-line >&2"}

	var stdout, stderr bytes.Buffer
	Execute(context.Background(), opts, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "out-line") {
		t.Errorf("expected stdout to contain out-line, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "err-line") {
		t.Errorf("expected stderr to contain err-line, got %q", stderr.String())
	}
}

func TestExecute_TriggerDetectsSystemdViaInvocationID(t *testing.T) {
	t.Setenv("INVOCATION_ID", "abc123")

	opts := testOptions(t)
	opts.Command = "true"

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})
	if result.Job.Trigger != job.TriggerSystemd {
		t.Fatalf("expected trigger systemd-timer, got %q", result.Job.Trigger)
	}
}

func TestExecute_TriggerDefaultsToManualWithoutSystemd(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")

	opts := testOptions(t)
	opts.Command = "true"

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})
	if result.Job.Trigger != job.TriggerManual {
		t.Fatalf("expected trigger manual, got %q", result.Job.Trigger)
	}
}

func TestExecute_ExplicitTriggerOverridesDetection(t *testing.T) {
	t.Setenv("INVOCATION_ID", "abc123")

	opts := testOptions(t)
	opts.Command = "true"
	opts.Trigger = job.TriggerCron

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})
	if result.Job.Trigger != job.TriggerCron {
		t.Fatalf("expected the explicit --trigger cron to win, got %q", result.Job.Trigger)
	}
}

// TestExecute_WiresTheRcloneExtractorByCommandName runs a fake "rclone"
// (a shell script placed first on PATH) that emits a real-shaped
// --use-json-log stats line, proving Execute's cmdName-based extractor
// selection actually reaches internal/job/extract end-to-end, not just in
// the extract package's own unit tests.
func TestExecute_WiresTheRcloneExtractorByCommandName(t *testing.T) {
	binDir := t.TempDir()
	script := `#!/bin/sh
echo '{"level":"notice","stats":{"bytes":500,"checks":1,"deletes":0,"errors":0,"renames":0,"transfers":2}}'
`
	scriptPath := filepath.Join(binDir, "rclone")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	opts := testOptions(t)
	opts.Command = "rclone"
	opts.Args = []string{"sync", "a", "b", "--use-json-log"}

	result := Execute(context.Background(), opts, &bytes.Buffer{}, &bytes.Buffer{})

	if result.Job.Stats["bytes_transferred"] != int64(500) {
		t.Fatalf("expected the rclone extractor to have run, got stats %+v", result.Job.Stats)
	}
}

func listenUnix(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}
