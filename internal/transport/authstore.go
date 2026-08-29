package transport

import (
	"context"
	"fmt"
	"sync"
)

// TokenStore resolves a presented bearer token to the host_id it's bound
// to (ADR-0008: "cada token está vinculado a un host_id"). It never stores
// or exposes the plaintext token — only HashToken's output.
type TokenStore interface {
	// Lookup returns the host_id bound to token, or ok=false if no stored
	// token matches.
	Lookup(ctx context.Context, token string) (hostID string, ok bool, err error)
}

// MemoryTokenStore is an in-memory TokenStore, for tests and for a hub
// that hasn't wired real persistence yet.
//
// Lookup iterates every stored hash and verifies each with Argon2id — a
// token can't be looked up by a fast index because the whole point of
// hashing is that the plaintext isn't derivable from what's stored. This
// is fine at the scale ADR-0004 targets (four hosts); a deployment with
// many more agents would want to have the client send an unauthenticated
// token-ID prefix alongside the bearer token so the server can narrow the
// candidate set before the expensive Argon2id verify. Not needed yet.
type MemoryTokenStore struct {
	mu      sync.RWMutex
	records []tokenRecord
}

type tokenRecord struct {
	hostID string
	hash   string
}

// NewMemoryTokenStore returns an empty store.
func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{}
}

// AddToken hashes plaintextToken with Argon2id and binds it to hostID.
// Returns the plaintext token unchanged, purely so a caller in a test or a
// `bita agent create`-style tool can generate-then-add in one call.
func (s *MemoryTokenStore) AddToken(hostID, plaintextToken string) error {
	hash, err := HashToken(plaintextToken)
	if err != nil {
		return fmt.Errorf("hashing token: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, tokenRecord{hostID: hostID, hash: hash})
	return nil
}

// Lookup implements TokenStore.
func (s *MemoryTokenStore) Lookup(ctx context.Context, token string) (string, bool, error) {
	s.mu.RLock()
	records := make([]tokenRecord, len(s.records))
	copy(records, s.records)
	s.mu.RUnlock()

	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		match, err := VerifyToken(token, r.hash)
		if err != nil {
			continue // a malformed stored hash shouldn't abort the whole lookup
		}
		if match {
			return r.hostID, true, nil
		}
	}
	return "", false, nil
}
