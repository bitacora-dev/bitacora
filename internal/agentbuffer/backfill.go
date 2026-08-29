package agentbuffer

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/time/rate"
)

// Sender sends one batch of items and returns an error if the hub didn't
// accept it. A real agent wires this to transport.Client.Send (wrapped to
// convert Items to a bitacorapb.Batch) — this package doesn't import
// transport, so the buffer stays testable without a network stack.
type Sender func(ctx context.Context, items []Item) error

// DefaultBackfillBatchSize is how many items Backfill sends per call to
// Sender.
const DefaultBackfillBatchSize = 200

// BackfillOptions configures Backfill.
type BackfillOptions struct {
	BatchSize int
	// Limiter throttles between batches (ADR-0008: "límite de tasa para
	// no saturar el hub al volver"). nil means unlimited.
	Limiter *rate.Limiter
}

// Backfill sends every buffered item through send, oldest first
// (ADR-0008: "en orden cronológico"), in batches. An item is removed from
// the buffer — via Ack — only after its batch's send call returns
// success: "el agente no borra de su buffer hasta recibir confirmación."
//
// If send fails partway through, Backfill returns that error immediately.
// Every batch that already succeeded was already acked, so calling
// Backfill again resumes from where it left off instead of resending
// already-confirmed data.
func (b *Buffer) Backfill(ctx context.Context, send Sender, opts BackfillOptions) error {
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBackfillBatchSize
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		items, err := b.oldestItems(batchSize)
		if err != nil {
			return fmt.Errorf("reading buffered items: %w", err)
		}
		if len(items) == 0 {
			return nil
		}

		if opts.Limiter != nil {
			if err := opts.Limiter.Wait(ctx); err != nil {
				return fmt.Errorf("rate limit wait: %w", err)
			}
		}

		if err := send(ctx, items); err != nil {
			return fmt.Errorf("sending batch: %w", err)
		}

		if err := b.Ack(items[len(items)-1].Seq); err != nil {
			return fmt.Errorf("acking sent batch: %w", err)
		}
	}
}

func (b *Buffer) oldestItems(n int) ([]Item, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []Item
	for _, seg := range b.sealed {
		if len(result) >= n {
			break
		}
		items, err := readSealedSegment(seg.path)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if len(result) >= n {
				break
			}
			result = append(result, it)
		}
	}
	if len(result) < n && b.active != nil {
		for _, it := range b.active.items {
			if len(result) >= n {
				break
			}
			result = append(result, it)
		}
	}
	return result, nil
}

// Ack removes every item with Seq <= upToSeq from the buffer — the hub has
// confirmed it has them, so they no longer need to survive a crash here.
func (b *Buffer) Ack(upToSeq uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var kept []sealedSegment
	for _, seg := range b.sealed {
		switch {
		case seg.maxSeq <= upToSeq:
			if err := os.Remove(seg.path); err != nil && !os.IsNotExist(err) {
				return err
			}
			b.totalLen -= seg.count

		case seg.minSeq > upToSeq:
			kept = append(kept, seg)

		default: // partial overlap
			items, err := readSealedSegment(seg.path)
			if err != nil {
				return err
			}
			var remaining []Item
			removed := 0
			for _, it := range items {
				if it.Seq <= upToSeq {
					removed++
					continue
				}
				remaining = append(remaining, it)
			}
			if len(remaining) == 0 {
				if err := os.Remove(seg.path); err != nil {
					return err
				}
				b.totalLen -= seg.count
				continue
			}
			if err := writeSealedSegment(seg.path, remaining); err != nil {
				return err
			}
			seg.count = len(remaining)
			seg.minSeq = remaining[0].Seq
			seg.maxSeq = remaining[len(remaining)-1].Seq
			if info, err := os.Stat(seg.path); err == nil {
				seg.byteSize = info.Size()
			}
			b.totalLen -= removed
			kept = append(kept, seg)
		}
	}
	b.sealed = kept

	if b.active != nil {
		var remaining []Item
		removed := 0
		for _, it := range b.active.items {
			if it.Seq <= upToSeq {
				removed++
				continue
			}
			remaining = append(remaining, it)
		}
		if removed > 0 {
			b.active.items = remaining
			if err := b.active.rewrite(); err != nil {
				return err
			}
			b.totalLen -= removed
		}
	}

	return nil
}
