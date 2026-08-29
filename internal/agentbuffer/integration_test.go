package agentbuffer

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// countingReceiver is a transport.BatchReceiver that fails every batch
// after maxBatchesOK successful ones — used to simulate the hub going down
// mid-stream without actually tearing down the TCP listener (which would
// also break the *next* real hub instance's ability to reuse the
// address in a test).
type countingReceiver struct {
	mu            sync.Mutex
	batchesOK     int
	maxBatchesOK  int
	itemsReceived int
}

func (r *countingReceiver) ReceiveBatch(ctx context.Context, hostID string, batch *bitacorapb.Batch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.batchesOK >= r.maxBatchesOK {
		return errAtCapacity
	}
	r.batchesOK++
	r.itemsReceived += len(batch.GetMetrics()) + len(batch.GetEvents()) + len(batch.GetLogLines())
	return nil
}

// items returns how many individual metrics/events/log lines this
// receiver has accepted so far — the acceptance test cross-checks this
// against the buffer's own item counts, so it must count items, not
// batches.
func (r *countingReceiver) items() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.itemsReceived
}

var errAtCapacity = &hubDownError{}

type hubDownError struct{}

func (*hubDownError) Error() string { return "hub is down" }

func startHub(t *testing.T, receiver transport.BatchReceiver, tokens transport.TokenStore) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error listening: %v", err)
	}
	srv := &transport.Server{
		Tokens:      tokens,
		Idempotency: transport.NewMemoryIdempotencyStore(),
		Receiver:    receiver,
	}
	httpSrv := srv.NewH2CServer(ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})
	return "http://" + ln.Addr().String()
}

// TestAcceptance_KillHubMidStream_SurvivesAgentRestart_ThenBackfillSucceeds
// is this task's literal acceptance criterion: "Matar el hub a mitad de
// stream, el buffer sobrevive a un reinicio del agente, y el test de
// backfill pasa." — exercised end to end over real transport.Server /
// transport.Client, a real WAL directory, and a real (simulated) agent
// restart in between.
func TestAcceptance_KillHubMidStream_SurvivesAgentRestart_ThenBackfillSucceeds(t *testing.T) {
	const hostID = "host-a"
	const token = "test-token"

	tokens := transport.NewMemoryTokenStore()
	if err := tokens.AddToken(hostID, token); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --- Phase 1: agent buffers data, starts streaming, hub dies mid-stream ---
	dir := t.TempDir()
	buf, err := Open(dir, WithSegmentBytes(64<<10)) // small segments so this exercises multiple files
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}

	ts := time.Now()
	const totalItems = 20
	for i := 0; i < totalItems; i++ {
		if _, err := buf.Append(logLineItem(hostID, "line", ts.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("unexpected error appending item %d: %v", i, err)
		}
	}

	receiver := &countingReceiver{maxBatchesOK: 2} // hub accepts 2 batches, then "goes down"
	hubURL := startHub(t, receiver, tokens)
	client := &transport.Client{BaseURL: hubURL, Token: token}
	sender := TransportSender(client, hostID)

	err = buf.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 5})
	if err == nil {
		t.Fatal("expected Backfill to fail once the hub starts rejecting batches")
	}

	sentBeforeCrash := receiver.items()
	if sentBeforeCrash == 0 {
		t.Fatal("expected at least some batches to have landed before the hub went down")
	}
	remainingAfterCrash := buf.Len()
	if remainingAfterCrash == 0 {
		t.Fatal("expected unsent items to still be buffered after the hub went down mid-stream")
	}
	if sentBeforeCrash+remainingAfterCrash != totalItems {
		t.Fatalf("expected sent+remaining to account for every item: sent=%d remaining=%d total=%d",
			sentBeforeCrash, remainingAfterCrash, totalItems)
	}

	// --- Phase 2: agent restarts. Not calling buf.Close() first is the
	// point — an unclean shutdown is exactly what "reinicio del agente"
	// after a hub crash usually looks like. ---
	buf2, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error reopening the buffer after restart: %v", err)
	}
	defer buf2.Close()

	if got := buf2.Len(); got != remainingAfterCrash {
		t.Fatalf("expected the buffer to survive the agent restart with %d items, got %d", remainingAfterCrash, got)
	}

	// --- Phase 3: hub comes back up, agent backfills the rest ---
	freshReceiver := &countingReceiver{maxBatchesOK: totalItems} // healthy hub now; batches, not items, but totalItems is a safe upper bound
	hubURL2 := startHub(t, freshReceiver, tokens)
	client2 := &transport.Client{BaseURL: hubURL2, Token: token}
	sender2 := TransportSender(client2, hostID)

	if err := buf2.Backfill(context.Background(), sender2, BackfillOptions{BatchSize: 5}); err != nil {
		t.Fatalf("expected the backfill against the recovered hub to succeed, got %v", err)
	}

	if got := buf2.Len(); got != 0 {
		t.Fatalf("expected the buffer to be fully drained after a successful backfill, got %d", got)
	}
	if freshReceiver.items() != remainingAfterCrash {
		t.Fatalf("expected the recovered hub to have received exactly the %d items that survived the crash, got %d",
			remainingAfterCrash, freshReceiver.items())
	}
}
