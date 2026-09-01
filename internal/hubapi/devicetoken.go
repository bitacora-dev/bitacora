package hubapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// pairingTTL bounds how long a QR pairing code stays claimable (ADR-0014).
const pairingTTL = 5 * time.Minute

// DeviceTokenStore holds device tokens (ADR-0014: "token de dispositivo,
// distinto del token de agente ... transferible por código QR"). Unlike
// transport.MemoryTokenStore, device tokens aren't bound to a host_id —
// the pairing QR path is meant to authenticate a browser/PWA reading the
// hub, not an agent writing to a specific host.
//
// Lookup mirrors transport.MemoryTokenStore: hashes can't be indexed, so
// it linearly scans and verifies each with Argon2id.
type DeviceTokenStore struct {
	mu     sync.RWMutex
	hashes []string

	pmu      sync.Mutex
	pairings map[string]*pairing
}

type pairing struct {
	token     string
	expiresAt time.Time
	claimed   bool
}

// NewDeviceTokenStore returns an empty store.
func NewDeviceTokenStore() *DeviceTokenStore {
	return &DeviceTokenStore{pairings: make(map[string]*pairing)}
}

// Lookup implements the same linear-scan-and-verify pattern as
// transport.MemoryTokenStore.Lookup.
func (s *DeviceTokenStore) Lookup(ctx context.Context, token string) (bool, error) {
	s.mu.RLock()
	hashes := make([]string, len(s.hashes))
	copy(hashes, s.hashes)
	s.mu.RUnlock()

	for _, h := range hashes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		match, err := transport.VerifyToken(token, h)
		if err != nil {
			continue // a malformed stored hash shouldn't abort the whole lookup
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

// HasAnyToken reports whether at least one device has ever been paired.
// The pairing handler uses this to decide whether a pairing request needs
// to already present a valid device token: the very first pairing (an
// empty store) has no existing device to present one from, so it's let
// through once — every pairing after that must be authenticated, or
// anyone reaching the hub over the network could mint themselves a
// device token for free.
func (s *DeviceTokenStore) HasAnyToken() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hashes) > 0
}

func (s *DeviceTokenStore) addToken(token string) error {
	hash, err := transport.HashToken(token)
	if err != nil {
		return fmt.Errorf("hashing device token: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes = append(s.hashes, hash)
	return nil
}

// Start begins a pairing: it mints a fresh device token, stores its hash
// immediately (so the token is valid the moment it's issued), and files a
// single-use, TTL-bounded pairing record under a short code meant to be
// embedded in a QR URL.
func (s *DeviceTokenStore) Start(ctx context.Context) (code string, token string, expiresAt time.Time, err error) {
	codeBytes := make([]byte, 4)
	if _, err = rand.Read(codeBytes); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generating pairing code: %w", err)
	}
	code = hex.EncodeToString(codeBytes)

	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generating device token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(tokenBytes)

	if err = s.addToken(token); err != nil {
		return "", "", time.Time{}, err
	}

	expiresAt = time.Now().Add(pairingTTL)

	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.gcLocked()
	s.pairings[code] = &pairing{token: token, expiresAt: expiresAt}

	return code, token, expiresAt, nil
}

// Claim resolves a pairing code to its device token exactly once. A
// missing, expired, or already-claimed code all report ok=false — the
// caller (the HTTP handler) decides how to translate that into a status
// code; nothing here distinguishes the reason, so an attacker probing
// codes can't tell "wrong" from "used up".
func (s *DeviceTokenStore) Claim(ctx context.Context, code string) (token string, ok bool) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.gcLocked()

	p, found := s.pairings[code]
	if !found || p.claimed || time.Now().After(p.expiresAt) {
		return "", false
	}
	p.claimed = true
	return p.token, true
}

// gcLocked drops expired or already-claimed pairing records. Called from
// Start and Claim, both of which already hold pmu; there's no background
// goroutine (low-traffic single-maintainer tool, per ADR-0014's context).
func (s *DeviceTokenStore) gcLocked() {
	now := time.Now()
	for code, p := range s.pairings {
		if p.claimed || now.After(p.expiresAt) {
			delete(s.pairings, code)
		}
	}
}
