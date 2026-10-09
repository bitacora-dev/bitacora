// Package actionconfirm owns the human-only, second-factor confirmation
// boundary for the fixed package-operation channel in ADR-0022.
package actionconfirm

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// DefaultTokenTTL intentionally keeps the second factor short-lived. It is
// independent of browser sessions, device tokens and ingest credentials.
const DefaultTokenTTL = 2 * time.Minute

var (
	ErrInvalidToken = errors.New("invalid action token")
	ErrTokenUsed    = errors.New("action token already used")
	ErrTokenExpired = errors.New("action token expired")
)

type AuditResult string

const (
	AuditResultConfirmed      AuditResult = "confirmed"
	AuditResultDeliveryFailed AuditResult = "delivery_failed"
)

// IssueInput is deliberately limited to the human identity and the closed
// operation binding. It has no place for inventory, logs, alerts, tags, text
// or executable arguments.
type IssueInput struct {
	Subject       string
	HostID        string
	Operation     bitacorapb.PackageOperation
	NetworkOrigin string
}

type ConfirmInput struct {
	Subject       string
	HostID        string
	Operation     bitacorapb.PackageOperation
	RequestID     string
	Token         string
	NetworkOrigin string
}

type IssuedToken struct {
	RequestID string
	Token     string
	ExpiresAt time.Time
}

// TokenRecord contains only a hash, never the action-token plaintext.
type TokenRecord struct {
	ID        string
	RequestID string
	TokenHash string
	Subject   string
	HostID    string
	Operation bitacorapb.PackageOperation
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type AuditEvent struct {
	ID            string
	RequestID     string
	Subject       string
	HostID        string
	Operation     bitacorapb.PackageOperation
	NetworkOrigin string
	Result        AuditResult
	OccurredAt    time.Time
}

type PendingOrder struct {
	RequestID string
	HostID    string
	Operation bitacorapb.PackageOperation
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Backend is deliberately narrow. It has no update/delete audit API: audit
// records are append-only and action-token consumption is separate state.
type Backend interface {
	StoreActionToken(ctx context.Context, token TokenRecord) error
	FindActionTokens(ctx context.Context, requestID string) ([]TokenRecord, error)
	ConsumeActionToken(ctx context.Context, tokenID string, usedAt time.Time) (bool, error)
	AppendActionAudit(ctx context.Context, event AuditEvent) error
	QueuePendingOrder(ctx context.Context, order PendingOrder) error
	NextPendingOrder(ctx context.Context, hostID string, now time.Time) *bitacorapb.PendingPackageOperation
}

// Store validates the separate action token before an order can enter the
// agent pull channel. Without an injected backend it uses memory only for
// isolated unit tests; production must inject persistent SQLite storage.
type Store struct {
	backend Backend
	now     func() time.Time
}

func NewStore(backends ...Backend) *Store {
	backend := Backend(newMemoryBackend())
	if len(backends) > 0 && backends[0] != nil {
		backend = backends[0]
	}
	return &Store{backend: backend, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) Issue(ctx context.Context, input IssueInput) (IssuedToken, error) {
	if err := validateBinding(input.Subject, input.HostID, input.Operation); err != nil {
		return IssuedToken{}, err
	}
	requestID, err := randomValue(18)
	if err != nil {
		return IssuedToken{}, err
	}
	plaintext, err := randomValue(32)
	if err != nil {
		return IssuedToken{}, err
	}
	hash, err := transport.HashToken(plaintext)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("hashing action token: %w", err)
	}
	now := s.now().UTC()
	record := TokenRecord{
		ID:        requestID,
		RequestID: requestID,
		TokenHash: hash,
		Subject:   input.Subject,
		HostID:    input.HostID,
		Operation: input.Operation,
		ExpiresAt: now.Add(DefaultTokenTTL),
		CreatedAt: now,
	}
	if err := s.backend.StoreActionToken(ctx, record); err != nil {
		return IssuedToken{}, fmt.Errorf("storing action token: %w", err)
	}
	return IssuedToken{RequestID: requestID, Token: plaintext, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Store) Confirm(ctx context.Context, input ConfirmInput) error {
	if err := validateBinding(input.Subject, input.HostID, input.Operation); err != nil {
		return err
	}
	if input.RequestID == "" || input.Token == "" {
		return ErrInvalidToken
	}
	records, err := s.backend.FindActionTokens(ctx, input.RequestID)
	if err != nil {
		return fmt.Errorf("finding action token: %w", err)
	}
	now := s.now().UTC()
	var matched *TokenRecord
	for i := range records {
		record := &records[i]
		match, verifyErr := transport.VerifyToken(input.Token, record.TokenHash)
		if verifyErr != nil || !match {
			continue
		}
		matched = record
		break
	}
	if matched == nil {
		return ErrInvalidToken
	}
	if matched.UsedAt != nil {
		return ErrTokenUsed
	}
	if !now.Before(matched.ExpiresAt) {
		return ErrTokenExpired
	}
	if matched.Subject != input.Subject || matched.HostID != input.HostID || matched.Operation != input.Operation {
		return ErrInvalidToken
	}
	consumed, err := s.backend.ConsumeActionToken(ctx, matched.ID, now)
	if err != nil {
		return fmt.Errorf("consuming action token: %w", err)
	}
	if !consumed {
		return ErrTokenUsed
	}

	audit := AuditEvent{
		ID: randomAuditID(), RequestID: input.RequestID, Subject: input.Subject,
		HostID: input.HostID, Operation: input.Operation, NetworkOrigin: input.NetworkOrigin,
		Result: AuditResultConfirmed, OccurredAt: now,
	}
	// This append happens before the order is made visible to an agent. If the
	// delivery step fails, a second append records that outcome without altering
	// this original immutable record.
	if err := s.backend.AppendActionAudit(ctx, audit); err != nil {
		return fmt.Errorf("appending confirmation audit: %w", err)
	}
	if err := s.backend.QueuePendingOrder(ctx, PendingOrder{
		RequestID: input.RequestID, HostID: input.HostID, Operation: input.Operation,
		ExpiresAt: matched.ExpiresAt, CreatedAt: now,
	}); err != nil {
		failure := audit
		failure.ID = randomAuditID()
		failure.Result = AuditResultDeliveryFailed
		failure.OccurredAt = s.now().UTC()
		if auditErr := s.backend.AppendActionAudit(ctx, failure); auditErr != nil {
			return fmt.Errorf("queueing pending order: %v; appending delivery failure audit: %w", err, auditErr)
		}
		return fmt.Errorf("queueing pending order: %w", err)
	}
	return nil
}

// NextPendingOrder satisfies transport.PendingOrderSource. The protobuf is a
// closed operation contract; no arbitrary command reaches the agent.
func (s *Store) NextPendingOrder(ctx context.Context, hostID string) *bitacorapb.PendingPackageOperation {
	return s.backend.NextPendingOrder(ctx, hostID, s.now().UTC())
}

func validateBinding(subject, hostID string, operation bitacorapb.PackageOperation) error {
	if subject == "" || hostID == "" || !allowedOperation(operation) {
		return ErrInvalidToken
	}
	return nil
}

func allowedOperation(operation bitacorapb.PackageOperation) bool {
	switch operation {
	case bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES:
		return true
	default:
		return false
	}
}

func randomValue(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating action token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomAuditID() string {
	value, err := randomValue(18)
	if err != nil {
		// Audit IDs do not authorize anything. A time-based fallback preserves
		// the append attempt if the host entropy source has a transient failure.
		return fmt.Sprintf("audit-%d", time.Now().UnixNano())
	}
	return value
}

// memoryBackend exists only as the default test backend, following the
// DeviceTokenStore pattern. Production uses SQLiteStore as the backend.
type memoryBackend struct {
	mu         sync.Mutex
	tokens     map[string]TokenRecord
	audits     []AuditEvent
	orders     []PendingOrder
	queueErr   error
	operations []string
}

func newMemoryBackend() *memoryBackend { return &memoryBackend{tokens: make(map[string]TokenRecord)} }

func (m *memoryBackend) StoreActionToken(_ context.Context, token TokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token.ID] = token
	return nil
}

func (m *memoryBackend) FindActionTokens(_ context.Context, requestID string) ([]TokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var records []TokenRecord
	for _, record := range m.tokens {
		if record.RequestID == requestID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (m *memoryBackend) ConsumeActionToken(_ context.Context, tokenID string, usedAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.tokens[tokenID]
	if !ok || record.UsedAt != nil {
		return false, nil
	}
	record.UsedAt = &usedAt
	m.tokens[tokenID] = record
	return true, nil
}

func (m *memoryBackend) AppendActionAudit(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, event)
	m.operations = append(m.operations, "audit:"+string(event.Result))
	return nil
}

func (m *memoryBackend) QueuePendingOrder(_ context.Context, order PendingOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operations = append(m.operations, "queue")
	if m.queueErr != nil {
		return m.queueErr
	}
	m.orders = append(m.orders, order)
	return nil
}

func (m *memoryBackend) NextPendingOrder(_ context.Context, hostID string, now time.Time) *bitacorapb.PendingPackageOperation {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, order := range m.orders {
		if order.HostID == hostID && now.Before(order.ExpiresAt) {
			return &bitacorapb.PendingPackageOperation{Operation: order.Operation, RequestId: order.RequestID, ExpiresAtMs: order.ExpiresAt.UnixMilli()}
		}
	}
	return nil
}

func (m *memoryBackend) AuditEvents() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent(nil), m.audits...)
}
