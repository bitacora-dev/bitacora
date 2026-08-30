package transport

import (
	"sync"

	"golang.org/x/time/rate"
)

// PerTokenLimiter rate-limits ingest requests per host_id, so one buggy
// agent can't overwhelm the hub (ADR-0008: "el endpoint de ingesta
// necesita límite de tasa por token").
type PerTokenLimiter struct {
	rps   rate.Limit
	burst int

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewPerTokenLimiter allows up to burst requests immediately per host,
// refilling at rps requests/second thereafter.
func NewPerTokenLimiter(rps float64, burst int) *PerTokenLimiter {
	return &PerTokenLimiter{
		rps:      rate.Limit(rps),
		burst:    burst,
		limiters: make(map[string]*rate.Limiter),
	}
}

// Allow reports whether a request for hostID may proceed right now.
func (l *PerTokenLimiter) Allow(hostID string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[hostID]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[hostID] = lim
	}
	l.mu.Unlock()

	return lim.Allow()
}
