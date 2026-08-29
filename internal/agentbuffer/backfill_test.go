package agentbuffer

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestBackfill_SendsAllItemsInOrderThenEmpties(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	for i := 0; i < 10; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	var sent []Item
	sender := func(ctx context.Context, items []Item) error {
		sent = append(sent, items...)
		return nil
	}

	if err := b.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sent) != 10 {
		t.Fatalf("expected all 10 items sent, got %d", len(sent))
	}
	for i, it := range sent {
		if int(it.Seq) != i+1 {
			t.Fatalf("expected chronological order, got seq %d at position %d", it.Seq, i)
		}
	}
	if got := b.Len(); got != 0 {
		t.Fatalf("expected the buffer to be empty after a fully successful backfill, got Len()=%d", got)
	}
}

func TestBackfill_StopsAtFirstFailureAndKeepsUnsentItems(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	for i := 0; i < 9; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	callCount := 0
	sender := func(ctx context.Context, items []Item) error {
		callCount++
		if callCount == 2 {
			return errors.New("hub unreachable")
		}
		return nil
	}

	err = b.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 3})
	if err == nil {
		t.Fatal("expected Backfill to return the sender's error")
	}

	// First batch of 3 succeeded and should be acked (gone); the rest
	// (6 items) must still be there — "el agente no borra de su buffer
	// hasta recibir confirmación."
	if got := b.Len(); got != 6 {
		t.Fatalf("expected 6 unsent items to remain after a mid-stream failure, got %d", got)
	}
}

func TestBackfill_ResumesAfterAPreviousFailure(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	for i := 0; i < 6; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	fail := true
	var totalSent int
	sender := func(ctx context.Context, items []Item) error {
		if fail {
			fail = false
			return errors.New("transient failure")
		}
		totalSent += len(items)
		return nil
	}

	if err := b.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 3}); err == nil {
		t.Fatal("expected the first Backfill call to fail")
	}
	if got := b.Len(); got != 6 {
		t.Fatalf("expected nothing acked on the very first failed batch, got Len()=%d", got)
	}

	if err := b.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 3}); err != nil {
		t.Fatalf("expected the retried backfill to succeed, got %v", err)
	}
	if got := b.Len(); got != 0 {
		t.Fatalf("expected the buffer to be fully drained after the successful retry, got %d", got)
	}
	if totalSent != 6 {
		t.Fatalf("expected all 6 items eventually sent exactly once, got %d", totalSent)
	}
}

func TestBackfill_EmptyBufferIsANoOp(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	called := false
	sender := func(ctx context.Context, items []Item) error {
		called = true
		return nil
	}

	if err := b.Backfill(context.Background(), sender, BackfillOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected the sender never to be called for an empty buffer")
	}
}

func TestBackfill_RespectsRateLimiter(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	var timestamps []time.Time
	sender := func(ctx context.Context, items []Item) error {
		timestamps = append(timestamps, time.Now())
		return nil
	}

	limiter := rate.NewLimiter(rate.Every(50*time.Millisecond), 1)
	start := time.Now()
	if err := b.Backfill(context.Background(), sender, BackfillOptions{BatchSize: 1, Limiter: limiter}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(timestamps) != 4 {
		t.Fatalf("expected 4 batches, got %d", len(timestamps))
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected the rate limiter to space out 4 batches by at least ~150ms total, took %v", elapsed)
	}
}
