// SPDX-License-Identifier: Apache-2.0

// Package execwrap runs a wrapped command for bitacora-run (ADR-0010),
// capturing its stdout/stderr, real exit code and, when it was killed
// rather than exited, the signal — while still passing the child's output
// through to the wrapper's own stdout/stderr, since bitacora-run must
// change nothing about how the command behaves for whoever invoked it.
//
// This is one of the two places in the codebase allowed to import
// "os/exec" (ADR-0012); the other is the helpers under internal/smarthelper.
package execwrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// defaultGracePeriod is used when Options.GracePeriod is zero.
const defaultGracePeriod = 5 * time.Second

// Result is everything bitacora-run needs to know about how the wrapped
// command ran.
type Result struct {
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Signal     string // non-empty iff the process was killed by a signal
	SignalNum  int    // the signal's number, e.g. 9 for SIGKILL; 0 when Signal is empty
	TimedOut   bool   // true iff Options.Timeout elapsed before the command finished
	Stdout     []byte
	Stderr     []byte
}

// Options configures Run.
type Options struct {
	// Timeout is the maximum time to let the command run before sending
	// SIGTERM. Zero means no timeout.
	Timeout time.Duration

	// GracePeriod is how long to wait after SIGTERM before escalating to
	// SIGKILL, on timeout or context cancellation. Defaults to 5s.
	GracePeriod time.Duration

	// PassthroughStdout and PassthroughStderr, if set, receive a live copy
	// of the child's output as it happens, in addition to what Run
	// captures into Result.
	PassthroughStdout io.Writer
	PassthroughStderr io.Writer

	// OnStart, if set, is called with the child's PID right after it
	// starts — the hook tests use to simulate an external kill (ADR-0010's
	// required "killed by SIGKILL mid-run" test) without Run exposing
	// *exec.Cmd itself.
	OnStart func(pid int)
}

// Run starts argv[0] with argv[1:] as arguments, waits for it to finish
// (or for Options.Timeout / ctx to end it first), and returns what
// happened. Run itself only fails if the command couldn't even be
// started (bad binary, permissions) — everything else, including the
// child dying non-zero or by a signal, is reported in Result, not err,
// because a wrapper failing internally must never look like the wrapped
// command failed.
func Run(ctx context.Context, argv []string, opts Options) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("execwrap: empty argv")
	}

	cmd := exec.Command(argv[0], argv[1:]...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = teeWriter(opts.PassthroughStdout, &stdoutBuf)
	cmd.Stderr = teeWriter(opts.PassthroughStderr, &stderrBuf)

	result := Result{StartedAt: time.Now().UTC()}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("execwrap: starting %q: %w", argv[0], err)
	}
	if opts.OnStart != nil {
		opts.OnStart(cmd.Process.Pid)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	var timeoutC <-chan time.Time
	if opts.Timeout > 0 {
		timer := time.NewTimer(opts.Timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	var err error
	select {
	case err = <-waitErr:
	case <-timeoutC:
		result.TimedOut = true
		err = terminateGracefully(cmd, waitErr, opts.GracePeriod)
	case <-ctx.Done():
		err = terminateGracefully(cmd, waitErr, opts.GracePeriod)
	}

	result.FinishedAt = time.Now().UTC()
	result.Stdout = stdoutBuf.Bytes()
	result.Stderr = stderrBuf.Bytes()
	applyExitState(&result, err)

	return result, nil
}

// terminateGracefully sends SIGTERM, then SIGKILL if the process hasn't
// exited within grace, returning whatever cmd.Wait() ultimately reports.
func terminateGracefully(cmd *exec.Cmd, waitErr <-chan error, grace time.Duration) error {
	if grace <= 0 {
		grace = defaultGracePeriod
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case err := <-waitErr:
		return err
	case <-timer.C:
		_ = cmd.Process.Signal(syscall.SIGKILL)
		return <-waitErr
	}
}

// applyExitState fills in ExitCode and Signal from the error cmd.Wait()
// returned (nil on a clean exit(0)).
func applyExitState(r *Result, waitErr error) {
	if waitErr == nil {
		r.ExitCode = 0
		return
	}

	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		// The command never got a chance to report an exit code at all
		// (e.g. cmd.Start succeeded but Wait failed for some other
		// reason) — treat like a generic failure.
		r.ExitCode = -1
		return
	}

	r.ExitCode = exitErr.ExitCode()
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		r.Signal = ws.Signal().String()
		r.SignalNum = int(ws.Signal())
	}
}

func teeWriter(passthrough io.Writer, buf *bytes.Buffer) io.Writer {
	if passthrough == nil {
		return buf
	}
	return io.MultiWriter(passthrough, buf)
}
