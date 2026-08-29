// bitacora-run is the wrapper ADR-0010 puts in front of any periodic
// command (backups, syncs, scrubs...) to get real instrumentation instead
// of parsing logs after the fact: real exit code, real signal on an abrupt
// death, and per-tool statistics.
//
// Hard requirement from the ADR: "ante cualquier error interno, ejecuta el
// comando igualmente" — a bug or outage in bitacora-run's own bookkeeping
// must never stop the wrapped command from running. That's why Execute
// below launches the child as its very first move and treats everything
// after Wait() (trigger detection already happened before launch and is
// itself infallible) as best-effort: an extractor panic or a spool write
// failure is logged to stderr, never allowed to change the exit code the
// caller (cron, systemd) sees.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/job"
	"github.com/bitacora-dev/bitacora/internal/job/extract"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DefaultGracePeriod is how long Execute waits between SIGTERM and SIGKILL
// once --timeout elapses (ADR-0010: "SIGTERM seguido de SIGKILL tras un
// margen de gracia").
const DefaultGracePeriod = 5 * time.Second

// DefaultSpoolDir is where jobs land when the agent socket is unreachable.
const DefaultSpoolDir = "/var/lib/bitacora/spool/jobs"

// Options configures one bitacora-run invocation.
type Options struct {
	JobName     string
	Trigger     string // "" = auto-detect (see detectTrigger)
	Timeout     time.Duration
	GracePeriod time.Duration
	SocketPath  string
	SpoolDir    string
	HostIDPath  string
	Command     string
	Args        []string
}

// Result is what Execute produces: the Job it recorded and the exit code
// the caller should propagate.
type Result struct {
	Job      job.Job
	ExitCode int
}

// Execute runs opts.Command under instrumentation and reports the
// resulting Job. stdout/stderr receive the child's own output, passed
// through live — a wrapped backup's output must still reach the terminal
// or cron's mail, exactly as if bitacora-run weren't there.
func Execute(ctx context.Context, opts Options, stdout, stderr io.Writer) Result {
	started := time.Now().UTC()
	jobID := ulid.Make().String()

	trigger := opts.Trigger
	if trigger == "" {
		trigger = detectTrigger()
	}

	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Stdout = io.MultiWriter(stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(stderr, &errBuf)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		// The command itself couldn't be launched (not found, not
		// executable, ...). That's a real failure of the wrapped command,
		// not an internal bitacora-run error — report it as such.
		j := job.Job{
			ID: jobID, JobName: opts.JobName, HostID: loadHostID(opts.HostIDPath, stderr),
			StartedAt: started, FinishedAt: time.Now().UTC(), Status: job.StatusFailed,
			ExitCode: -1, Trigger: trigger, Schema: job.CurrentSchemaVersion,
		}
		reportBestEffort(ctx, opts, j, stderr)
		fmt.Fprintf(stderr, "bitacora-run: starting %q: %v\n", opts.Command, err)
		return Result{Job: j, ExitCode: 127}
	}

	status, exitCode, signal := wait(ctx, cmd, opts.Timeout, opts.GracePeriod)
	finished := time.Now().UTC()

	j := job.Job{
		ID:              jobID,
		JobName:         opts.JobName,
		HostID:          loadHostID(opts.HostIDPath, stderr),
		StartedAt:       started,
		FinishedAt:      finished,
		DurationSeconds: finished.Sub(started).Seconds(),
		Status:          status,
		ExitCode:        exitCode,
		Signal:          signal,
		Trigger:         trigger,
		Schema:          job.CurrentSchemaVersion,
	}

	applyStatsBestEffort(&j, opts.Command, opts.Args, outBuf.Bytes(), errBuf.Bytes(), stderr)
	reportBestEffort(ctx, opts, j, stderr)

	return Result{Job: j, ExitCode: exitCode}
}

// wait waits for cmd to finish, enforcing timeout (SIGTERM, then SIGKILL
// after gracePeriod) if timeout > 0. It signals the whole process group,
// not just the direct child, so a wrapped shell pipeline actually dies.
func wait(ctx context.Context, cmd *exec.Cmd, timeout, gracePeriod time.Duration) (status job.Status, exitCode int, signal string) {
	if gracePeriod <= 0 {
		gracePeriod = DefaultGracePeriod
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timeoutC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	select {
	case err := <-done:
		return statusFromWaitErr(err)
	case <-timeoutC:
		signalGroup(cmd, syscall.SIGTERM)
		select {
		case err := <-done:
			_, exitCode, signal = statusFromWaitErr(err)
			return job.StatusTimeout, exitCode, signal
		case <-time.After(gracePeriod):
			signalGroup(cmd, syscall.SIGKILL)
			err := <-done
			_, exitCode, signal = statusFromWaitErr(err)
			return job.StatusTimeout, exitCode, signal
		}
	case <-ctx.Done():
		signalGroup(cmd, syscall.SIGTERM)
		select {
		case err := <-done:
			return statusFromWaitErr(err)
		case <-time.After(gracePeriod):
			signalGroup(cmd, syscall.SIGKILL)
			err := <-done
			_, exitCode, signal = statusFromWaitErr(err)
			return job.StatusKilled, exitCode, signal
		}
	}
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	// Setpgid:true makes the child its own process group leader, so its
	// pgid equals its pid; signaling -pgid reaches every process in the
	// group, not just the direct child (e.g. a wrapped `sh -c '...'`).
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// statusFromWaitErr classifies a completed process: clean exit -> success
// or failed by exit code; died by an external signal (not one bitacora-run
// itself sent for a timeout) -> killed. The timeout case overrides this
// classification in wait, since only it knows a kill was self-inflicted.
func statusFromWaitErr(err error) (status job.Status, exitCode int, signal string) {
	if err == nil {
		return job.StatusSuccess, 0, ""
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		// Wait() itself failed for a reason unrelated to the child's exit
		// (e.g. I/O error) — record it as failed with no usable exit code.
		return job.StatusFailed, -1, ""
	}

	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return job.StatusKilled, -1, signalName(ws.Signal())
	}

	code := exitErr.ExitCode()
	if code == 0 {
		return job.StatusSuccess, 0, ""
	}
	return job.StatusFailed, code, ""
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGTERM: "SIGTERM",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGPIPE: "SIGPIPE",
}

func signalName(s syscall.Signal) string {
	if name, ok := signalNames[s]; ok {
		return name
	}
	return "signal " + strconv.Itoa(int(s))
}

// loadHostID never blocks or fails the run: an unreadable/unwritable
// host_id file means an empty HostID on the Job, logged to stderr, not a
// reason to skip running the wrapped command.
func loadHostID(path string, stderr io.Writer) string {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "bitacora-run: recovered from panic loading host_id: %v\n", r)
		}
	}()

	id, err := schema.LoadOrCreateHostID(path)
	if err != nil {
		fmt.Fprintf(stderr, "bitacora-run: loading host_id: %v\n", err)
		return ""
	}
	return id
}

// applyStatsBestEffort selects and runs the extractor for cmdName, and
// always sets OutputLines via Generic — a parse failure or panic in a
// tool-specific extractor degrades the Job's Stats, never the Job itself.
func applyStatsBestEffort(j *job.Job, cmdName string, args []string, stdout, stderr []byte, errOut io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(errOut, "bitacora-run: recovered from panic in extractor for %q: %v\n", cmdName, r)
		}
	}()

	genericExtractor := extract.Generic{}
	if generic, err := genericExtractor.Extract(stdout, stderr); err == nil {
		if v, ok := generic["stdout_lines"].(int); ok {
			j.OutputLines = v
		}
	}

	e := extract.Select(cmdName, args)
	if _, isGeneric := e.(extract.Generic); isGeneric {
		return
	}

	stats, err := e.Extract(stdout, stderr)
	if err != nil {
		fmt.Fprintf(errOut, "bitacora-run: extractor for %q failed: %v\n", cmdName, err)
		return
	}
	j.Stats = stats
}

// reportBestEffort delivers j to the agent (or spool). Any failure here —
// including both the socket and the spool being unavailable — is logged,
// never propagated: by this point the wrapped command has already run to
// completion, and losing its instrumentation is strictly better than
// pretending the run itself failed.
func reportBestEffort(ctx context.Context, opts Options, j job.Job, stderr io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "bitacora-run: recovered from panic reporting job %s: %v\n", j.ID, r)
		}
	}()

	reportCtx, cancel := context.WithTimeout(ctx, job.DefaultDialTimeout+time.Second)
	defer cancel()

	if _, err := job.Report(reportCtx, opts.SocketPath, opts.SpoolDir, j, job.DefaultDialTimeout); err != nil {
		fmt.Fprintf(stderr, "bitacora-run: reporting job %s: %v\n", j.ID, err)
	}
}

// detectTrigger auto-detects systemd via $INVOCATION_ID, which systemd
// sets for every unit it runs (timers included) — a real, documented
// mechanism, not a heuristic. Reliable automatic cron detection has no
// equivalent: cron doesn't mark its children in any stable way, so this
// defaults to manual and expects --trigger cron from the crontab entry
// instead of guessing. See this package's README.
func detectTrigger() string {
	if os.Getenv("INVOCATION_ID") != "" {
		return job.TriggerSystemd
	}
	return job.TriggerManual
}
