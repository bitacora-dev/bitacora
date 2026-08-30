package transport

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// recordingReceiver is a BatchReceiver test double that records every
// batch it's handed.
type recordingReceiver struct {
	mu      sync.Mutex
	batches []*bitacorapb.Batch
}

func (r *recordingReceiver) ReceiveBatch(ctx context.Context, hostID string, batch *bitacorapb.Batch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, batch)
	return nil
}

func (r *recordingReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

// testServer starts a real Server on loopback, over real TCP, speaking h2c
// — not httptest.NewServer (which doesn't exercise the same HTTP/2
// cleartext path a real agent-to-hub connection uses on Tailscale).
func testServer(t *testing.T, tokens TokenStore, idempotency IdempotencyStore, receiver BatchReceiver) (baseURL string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error listening: %v", err)
	}

	srv := &Server{
		Tokens:      tokens,
		Idempotency: idempotency,
		Receiver:    receiver,
	}
	httpSrv := srv.NewH2CServer(ln.Addr().String())

	go func() {
		_ = httpSrv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})

	return "http://" + ln.Addr().String()
}

func sampleBatch(hostID string) *bitacorapb.Batch {
	return &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
		Metrics: []*bitacorapb.Metric{
			{Name: "bitacora_cpu_usage_ratio", HostId: hostID, Value: 0.42, TimestampMs: time.Now().UnixMilli()},
		},
	}
}

func TestEndToEnd_AgentPushesToHubOverLoopback(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	receiver := &recordingReceiver{}
	baseURL := testServer(t, tokens, NewMemoryIdempotencyStore(), receiver)

	client := &Client{BaseURL: baseURL, Token: "test-token"}
	batch := sampleBatch("host-a")

	resp, err := client.Send(context.Background(), batch)
	if err != nil {
		t.Fatalf("unexpected error sending batch: %v", err)
	}
	if resp.Duplicate {
		t.Fatal("expected the first send not to be marked duplicate")
	}
	if resp.LastOffset != batch.BatchId {
		t.Fatalf("expected last_offset %q, got %q", batch.BatchId, resp.LastOffset)
	}

	if receiver.count() != 1 {
		t.Fatalf("expected the hub to have received exactly 1 batch, got %d", receiver.count())
	}
}

func TestEndToEnd_DuplicateBatchIsANoOp(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	receiver := &recordingReceiver{}
	baseURL := testServer(t, tokens, NewMemoryIdempotencyStore(), receiver)

	client := &Client{BaseURL: baseURL, Token: "test-token"}
	batch := sampleBatch("host-a")

	if _, err := client.Send(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error on first send: %v", err)
	}

	// Retry the exact same batch (same batch_id) — the scenario a real
	// agent hits when a response is lost and it doesn't know whether the
	// first send landed.
	resp, err := client.Send(context.Background(), batch)
	if err != nil {
		t.Fatalf("unexpected error on retry: %v", err)
	}
	if !resp.Duplicate {
		t.Fatal("expected the retried batch to be reported as a duplicate")
	}

	if receiver.count() != 1 {
		t.Fatalf("expected the hub to have received the batch exactly once despite the retry, got %d", receiver.count())
	}
}

func TestEndToEnd_RejectsUnknownToken(t *testing.T) {
	tokens := NewMemoryTokenStore()
	receiver := &recordingReceiver{}
	baseURL := testServer(t, tokens, NewMemoryIdempotencyStore(), receiver)

	client := &Client{BaseURL: baseURL, Token: "no-such-token"}
	if _, err := client.Send(context.Background(), sampleBatch("host-a")); err == nil {
		t.Fatal("expected an unknown token to be rejected")
	}
	if receiver.count() != 0 {
		t.Fatal("expected nothing to reach the receiver for a rejected token")
	}
}

func TestEndToEnd_RejectsBatchHostIDMismatchedWithToken(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	receiver := &recordingReceiver{}
	baseURL := testServer(t, tokens, NewMemoryIdempotencyStore(), receiver)

	client := &Client{BaseURL: baseURL, Token: "test-token"}
	// The token is bound to host-a, but the batch claims to be from host-b
	// — exactly the "compromised agent forges another host's data" case
	// ADR-0008 says must be rejected.
	if _, err := client.Send(context.Background(), sampleBatch("host-b")); err == nil {
		t.Fatal("expected a batch whose host_id doesn't match the token's bound host to be rejected")
	}
	if receiver.count() != 0 {
		t.Fatal("expected nothing to reach the receiver for a host_id mismatch")
	}
}

func TestEndToEnd_RejectsMissingBatchID(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	receiver := &recordingReceiver{}
	baseURL := testServer(t, tokens, NewMemoryIdempotencyStore(), receiver)

	client := &Client{BaseURL: baseURL, Token: "test-token"}
	batch := sampleBatch("host-a")
	batch.BatchId = ""

	if _, err := client.Send(context.Background(), batch); err == nil {
		t.Fatal("expected a batch with no batch_id to be rejected")
	}
}

func TestEndToEnd_RateLimiterRejectsBeyondBurst(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := &Server{
		Tokens:      tokens,
		Idempotency: NewMemoryIdempotencyStore(),
		Receiver:    &recordingReceiver{},
		Limiter:     NewPerTokenLimiter(0.001, 1),
	}
	httpSrv := srv.NewH2CServer(ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})

	client := &Client{BaseURL: "http://" + ln.Addr().String(), Token: "test-token"}

	if _, err := client.Send(context.Background(), sampleBatch("host-a")); err != nil {
		t.Fatalf("expected the first request within burst to succeed, got %v", err)
	}
	if _, err := client.Send(context.Background(), sampleBatch("host-a")); err == nil {
		t.Fatal("expected the second request beyond burst to be rate limited")
	}
}
