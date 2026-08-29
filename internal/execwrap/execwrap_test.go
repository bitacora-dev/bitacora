package execwrap

import (
	"bytes"
	"context"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRun_CapturesExitCodeAndOutput(t *testing.T) {
	result, err := Run(context.Background(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
	if result.Signal != "" {
		t.Errorf("Signal = %q, want empty", result.Signal)
	}
	if !strings.Contains(string(result.Stdout), "out") {
		t.Errorf("Stdout = %q, want to contain %q", result.Stdout, "out")
	}
	if !strings.Contains(string(result.Stderr), "err") {
		t.Errorf("Stderr = %q, want to contain %q", result.Stderr, "err")
	}
	if result.FinishedAt.Before(result.StartedAt) {
		t.Error("FinishedAt is before StartedAt")
	}
}

func TestRun_Passthrough(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := Run(context.Background(), []string{"sh", "-c", "echo out; echo err >&2"}, Options{
		PassthroughStdout: &stdout,
		PassthroughStderr: &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "out") {
		t.Errorf("passthrough stdout = %q, want to contain %q", stdout.String(), "out")
	}
	if !strings.Contains(stderr.String(), "err") {
		t.Errorf("passthrough stderr = %q, want to contain %q", stderr.String(), "err")
	}
}

// TestRun_KilledBySignal_MidRun is the test ADR-0010 requires explicitly:
// a process killed by SIGKILL mid-run (as an OOM killer would do) must be
// registered as killed, with the correct signal — something parsing a log
// after the fact could never determine.
func TestRun_KilledBySignal_MidRun(t *testing.T) {
	pidCh := make(chan int, 1)
	go func() {
		pid := <-pidCh
		time.Sleep(100 * time.Millisecond)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()

	result, err := Run(context.Background(), []string{"sleep", "5"}, Options{
		OnStart: func(pid int) { pidCh <- pid },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal == "" {
		t.Fatal("expected a non-empty Signal for a SIGKILL'd process")
	}
	if !strings.Contains(strings.ToLower(result.Signal), "kill") {
		t.Errorf("Signal = %q, want it to mention kill", result.Signal)
	}
	if result.TimedOut {
		t.Error("TimedOut should be false — this was an external kill, not our own timeout")
	}
}

func TestRun_Timeout_EscalatesToSIGKILL(t *testing.T) {
	// Traps SIGTERM so the only way this process ends is our own SIGKILL
	// escalation after GracePeriod.
	result, err := Run(context.Background(), []string{"sh", "-c", "trap '' TERM; sleep 5"}, Options{
		Timeout:     50 * time.Millisecond,
		GracePeriod: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.TimedOut {
		t.Error("expected TimedOut to be true")
	}
	if result.Signal == "" {
		t.Error("expected a non-empty Signal after SIGKILL escalation")
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := Run(ctx, []string{"sleep", "5"}, Options{GracePeriod: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal == "" {
		t.Error("expected the process to have been terminated by a signal")
	}
}

func TestRun_EmptyArgvErrors(t *testing.T) {
	if _, err := Run(context.Background(), nil, Options{}); err == nil {
		t.Fatal("expected an error for empty argv")
	}
}
