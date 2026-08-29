package transport

import (
	"context"
	"sync"
)

// IdempotencyStore records which batch IDs a host has already had ingested
// (ADR-0008: "el hub ignora lotes ya ingeridos"), so a retried or
// backfilled batch is safe to resend.
type IdempotencyStore interface {
	// MarkSeen records (hostID, batchID) as ingested. alreadySeen is true
	// if this exact pair was already recorded — the caller's cue to treat
	// the batch as a no-op instead of re-applying it.
	MarkSeen(ctx context.Context, hostID, batchID string) (alreadySeen bool, err error)
}

// MemoryIdempotencyStore is an in-memory IdempotencyStore. Real deployments
// will want this backed by the relational store so dedup survives a hub
// restart — not wired here; this task is the transport layer, not the
// storage integration (see the PR's followups).
type MemoryIdempotencyStore struct {
	mu   sync.Mutex
	seen map[string]struct{} // key: hostID + "\x1f" + batchID
}

// NewMemoryIdempotencyStore returns an empty store.
func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{seen: make(map[string]struct{})}
}

// MarkSeen implements IdempotencyStore.
func (s *MemoryIdempotencyStore) MarkSeen(ctx context.Context, hostID, batchID string) (bool, error) {
	key := hostID + "\x1f" + batchID

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return true, nil
	}
	s.seen[key] = struct{}{}
	return false, nil
}
