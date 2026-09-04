package hubapi

import (
	"context"
	"fmt"
	"sync"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// memoryDeviceTokenBackend is intentionally only the default for unit tests
// and callers that do not provide the hub's persistent backend.
type memoryDeviceTokenBackend struct {
	mu     sync.RWMutex
	hashes []string
}

func newMemoryDeviceTokenBackend() *memoryDeviceTokenBackend {
	return &memoryDeviceTokenBackend{}
}

func (s *memoryDeviceTokenBackend) AddDeviceToken(token string) error {
	hash, err := transport.HashToken(token)
	if err != nil {
		return fmt.Errorf("hashing device token: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes = append(s.hashes, hash)
	return nil
}

func (s *memoryDeviceTokenBackend) HasAnyDeviceToken(context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hashes) > 0, nil
}

func (s *memoryDeviceTokenBackend) LookupDeviceToken(ctx context.Context, token string) (bool, error) {
	s.mu.RLock()
	hashes := append([]string(nil), s.hashes...)
	s.mu.RUnlock()

	for _, hash := range hashes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		match, err := transport.VerifyToken(token, hash)
		if err != nil {
			continue
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}
