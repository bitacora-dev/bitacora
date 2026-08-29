package job

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingReceiver struct {
	received []Job
	err      error
}

func (r *recordingReceiver) ReceiveJob(ctx context.Context, j Job) error {
	r.received = append(r.received, j)
	return r.err
}

func startTestServer(t *testing.T, receiver Receiver) (socketPath string, stop func()) {
	t.Helper()
	// A short-prefixed temp dir, not t.TempDir(): macOS caps a Unix socket
	// path (sun_path) at 104 bytes, and t.TempDir() embeds the full test
	// name, which blows past that for the longer test names in this file.
	dir, err := os.MkdirTemp("", "bjs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath = filepath.Join(dir, "agent.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Receiver: receiver}
	done := make(chan struct{})
	go func() {
		srv.Serve(ctx, ln)
		close(done)
	}()

	return socketPath, func() {
		cancel()
		<-done
	}
}

func TestDeliver_SucceedsAgainstARealListener(t *testing.T) {
	receiver := &recordingReceiver{}
	socketPath, stop := startTestServer(t, receiver)
	defer stop()

	j := sampleJob()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Deliver(ctx, socketPath, j); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(receiver.received) != 1 || receiver.received[0].ID != j.ID {
		t.Fatalf("expected the server to have received the job, got %+v", receiver.received)
	}
}

func TestDeliver_FailsFastWhenNothingIsListening(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "no-such-agent.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := Deliver(ctx, socketPath, sampleJob())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error dialing a socket nothing listens on")
	}
	if elapsed > time.Second {
		t.Fatalf("expected Deliver to fail fast, took %v", elapsed)
	}
}

func TestDeliver_PropagatesReceiverError(t *testing.T) {
	receiver := &recordingReceiver{err: errors.New("storage full")}
	socketPath, stop := startTestServer(t, receiver)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Deliver(ctx, socketPath, sampleJob()); err == nil {
		t.Fatal("expected the receiver's error to propagate")
	}
}
