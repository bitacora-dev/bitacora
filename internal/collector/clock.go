package collector

import "time"

// Clock abstracts time so the runtime is deterministic in tests (ADR-0007:
// reloj inyectado, nunca time.Now() directamente).
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker abstracts time.Ticker so it can be faked in tests.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// SystemClock is the real wall-clock Clock, used in production.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// NewTicker returns a Ticker backed by time.NewTicker.
func (SystemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }
