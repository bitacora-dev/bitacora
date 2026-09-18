// Package agentactions validates the fixed package-action channel from
// ADR-0022. It never executes operations.
package agentactions

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

const MaxPendingPollInterval = 5 * time.Second

// Allowlist is loaded only from the local agent configuration. Both entries
// default to false, which leaves the command channel inert.
type Allowlist struct {
	RefreshPackageCache        bool `json:"refresh_package_cache"`
	ApplyPendingPackageUpdates bool `json:"apply_pending_package_updates"`
}

func (a Allowlist) Allows(operation bitacorapb.PackageOperation) bool {
	switch operation {
	case bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE:
		return a.RefreshPackageCache
	case bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES:
		return a.ApplyPendingPackageUpdates
	default:
		return false
	}
}

func (a Allowlist) Enabled() bool {
	return a.RefreshPackageCache || a.ApplyPendingPackageUpdates
}

type Decision string

const (
	DecisionAccepted        Decision = "accepted"
	DecisionRejected        Decision = "rejected"
	DecisionExpired         Decision = "expired"
	DecisionAlreadyConsumed Decision = "already_consumed"
)

// Manager accepts or rejects a received order and records its request ID.
// Acceptance is intentionally not execution; the privileged helper belongs to
// a later ADR-0022 task.
type Manager struct {
	mu        sync.Mutex
	allowlist Allowlist
	now       func() time.Time
	logf      func(string, ...any)
	consumed  map[string]struct{}
	pending   map[string]int64
}

func NewManager(allowlist Allowlist, logf func(string, ...any)) *Manager {
	if logf == nil {
		logf = log.Printf
	}
	return &Manager{
		allowlist: allowlist,
		now:       time.Now,
		logf:      logf,
		consumed:  make(map[string]struct{}),
		pending:   make(map[string]int64),
	}
}

// Handle validates a hub response. No caller-supplied parameters can reach an
// action: the protobuf message contains only the closed enum, a request ID and
// expiry metadata.
func (m *Manager) Handle(order *bitacorapb.PendingPackageOperation) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	if order == nil || order.GetRequestId() == "" || !m.allowlist.Allows(order.GetOperation()) {
		m.logf("bitacora-agent: rejected package operation")
		return DecisionRejected
	}
	if order.GetExpiresAtMs() <= m.now().UnixMilli() {
		m.logf("bitacora-agent: rejected expired package operation request %s", order.GetRequestId())
		return DecisionExpired
	}
	if _, ok := m.consumed[order.GetRequestId()]; ok {
		m.logf("bitacora-agent: rejected consumed package operation request %s", order.GetRequestId())
		return DecisionAlreadyConsumed
	}
	m.consumed[order.GetRequestId()] = struct{}{}
	m.pending[order.GetRequestId()] = order.GetExpiresAtMs()
	m.logf("bitacora-agent: accepted package operation %s request %s", order.GetOperation(), order.GetRequestId())
	return DecisionAccepted
}

// Resolve clears a pending request after a later execution helper reports a
// terminal result. It does not make the request reusable.
func (m *Manager) Resolve(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, requestID)
}

func (m *Manager) Enabled() bool { return m.allowlist.Enabled() }

func (m *Manager) PollInterval(normal time.Duration) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UnixMilli()
	for requestID, expiresAtMs := range m.pending {
		if expiresAtMs <= now {
			delete(m.pending, requestID)
			m.logf("bitacora-agent: expired pending package operation request %s", requestID)
		}
	}
	if m.allowlist.Enabled() && len(m.pending) > 0 && normal > MaxPendingPollInterval {
		return MaxPendingPollInterval
	}
	return normal
}

func (m *Manager) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("package actions enabled=%t pending=%d", m.allowlist.Enabled(), len(m.pending))
}
