package blackbox

import "time"

// Clock and Ticker mirror internal/collector's own abstractions, but are
// deliberately redefined here rather than imported: ADR-0011 requires the
// black box to be "camino de código separado del runtime de collectors
// [...] la única excepción admitida [al Sink] [...] debe sobrevivir a un
// agente degradado" — sharing a package with the collector runtime would
// undercut that independence for the sake of a few lines saved.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker abstracts time.Ticker so Run is deterministic in tests.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// SystemClock is the real wall-clock Clock used in production.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
func (SystemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }

// DefaultSampleInterval is ADR-0011's 1 Hz sampling rate.
const DefaultSampleInterval = time.Second

// DefaultSyncInterval is ADR-0011's "volcado a disco cada 5 s".
const DefaultSyncInterval = 5 * time.Second

// Run samples at sampleInterval and syncs to disk at syncInterval, until
// ctx is done. It never returns an error from a failed Sync — a stalled
// or degraded disk shouldn't stop recording into memory, only its
// durability, and that failure is reported through onSyncError rather
// than aborting the loop (nil onSyncError silently drops it, which is a
// legitimate choice for a component whose entire point is surviving
// everything else going wrong).
func Run(clock Clock, sampler *Sampler, rec *Recorder, sampleInterval, syncInterval time.Duration, onSyncError func(error)) func(stop <-chan struct{}) {
	if sampleInterval <= 0 {
		sampleInterval = DefaultSampleInterval
	}
	if syncInterval <= 0 {
		syncInterval = DefaultSyncInterval
	}

	return func(stop <-chan struct{}) {
		sampleTicker := clock.NewTicker(sampleInterval)
		defer sampleTicker.Stop()
		syncTicker := clock.NewTicker(syncInterval)
		defer syncTicker.Stop()

		for {
			select {
			case <-stop:
				return
			case now := <-sampleTicker.C():
				rec.Record(sampler.Sample(now))
			case <-syncTicker.C():
				if err := rec.Sync(); err != nil && onSyncError != nil {
					onSyncError(err)
				}
			}
		}
	}
}
