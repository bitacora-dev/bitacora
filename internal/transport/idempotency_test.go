package transport

import (
	"context"
	"testing"
)

func TestMemoryIdempotencyStore_FirstSeenThenDuplicate(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	ctx := context.Background()

	dup, err := store.MarkSeen(ctx, "host-a", "batch-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dup {
		t.Fatal("expected the first sighting of a batch not to be a duplicate")
	}

	dup, err = store.MarkSeen(ctx, "host-a", "batch-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dup {
		t.Fatal("expected the second sighting of the same batch to be a duplicate")
	}
}

func TestMemoryIdempotencyStore_ScopedPerHost(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	ctx := context.Background()

	if _, err := store.MarkSeen(ctx, "host-a", "batch-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dup, err := store.MarkSeen(ctx, "host-b", "batch-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dup {
		t.Fatal("expected the same batch_id from a different host not to collide")
	}
}
