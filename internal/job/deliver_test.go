package job

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestReport_UsesSocketWhenAgentIsUp(t *testing.T) {
	receiver := &recordingReceiver{}
	socketPath, stop := startTestServer(t, receiver)
	defer stop()

	spoolDir := t.TempDir()
	j := sampleJob()

	path, err := Report(context.Background(), socketPath, spoolDir, j, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != DeliveredViaSocket {
		t.Fatalf("expected delivery via socket, got %s", path)
	}
	if len(receiver.received) != 1 {
		t.Fatalf("expected the agent to have received the job, got %d", len(receiver.received))
	}

	spooled, err := ReadSpool(spoolDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 0 {
		t.Fatalf("expected nothing spooled when the socket delivery succeeded, got %d", len(spooled))
	}
}

// TestReport_FallsBackToSpoolWhenAgentIsDown is ADR-0010's mandatory test:
// "con el agente parado, el comando se ejecuta igual y el job aparece
// cuando el agente vuelve." This covers the "job aparece" half — Report
// falling back to the spool — and TestBackfill_DeliversSpooledJobsAndDrains
// below covers "cuando el agente vuelve."
func TestReport_FallsBackToSpoolWhenAgentIsDown(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "no-agent.sock")
	spoolDir := t.TempDir()
	j := sampleJob()

	path, err := Report(context.Background(), socketPath, spoolDir, j, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != DeliveredViaSpool {
		t.Fatalf("expected delivery via spool, got %s", path)
	}

	spooled, err := ReadSpool(spoolDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 1 || spooled[0].Job.ID != j.ID {
		t.Fatalf("expected the job to have been spooled, got %+v", spooled)
	}
}

func TestBackfill_DeliversSpooledJobsAndDrains(t *testing.T) {
	spoolDir := t.TempDir()

	j1 := sampleJob()
	j1.ID = "01J0000000000000000000AAAA"
	j2 := sampleJob()
	j2.ID = "01J0000000000000000000BBBB"

	// The agent was down for both: Report spools each one.
	downSocket := filepath.Join(t.TempDir(), "down.sock")
	if _, err := Report(context.Background(), downSocket, spoolDir, j1, 200*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := Report(context.Background(), downSocket, spoolDir, j2, 200*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The agent comes back.
	receiver := &recordingReceiver{}
	socketPath, stop := startTestServer(t, receiver)
	defer stop()

	sent, err := Backfill(context.Background(), socketPath, spoolDir, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent != 2 {
		t.Fatalf("expected 2 jobs backfilled, got %d", sent)
	}
	if len(receiver.received) != 2 {
		t.Fatalf("expected the agent to have received 2 jobs, got %d", len(receiver.received))
	}

	spooled, err := ReadSpool(spoolDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 0 {
		t.Fatalf("expected the spool to be drained, got %d remaining", len(spooled))
	}
}

func TestBackfill_StopsAtFirstFailureAndResumesLater(t *testing.T) {
	spoolDir := t.TempDir()

	j1 := sampleJob()
	j1.ID = "01J0000000000000000000AAAA"
	j2 := sampleJob()
	j2.ID = "01J0000000000000000000BBBB"
	if err := WriteSpool(spoolDir, j1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteSpool(spoolDir, j2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	downSocket := filepath.Join(t.TempDir(), "still-down.sock")
	sent, err := Backfill(context.Background(), downSocket, spoolDir, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error since nothing is listening")
	}
	if sent != 0 {
		t.Fatalf("expected 0 jobs sent, got %d", sent)
	}

	spooled, err := ReadSpool(spoolDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 2 {
		t.Fatalf("expected both jobs still spooled after a failed backfill, got %d", len(spooled))
	}
}
