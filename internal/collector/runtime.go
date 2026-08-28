package collector

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// maxBackoffMultiplier caps exponential backoff at 10x the base interval
// (ADR-0007: "×2 hasta un máximo de 10 intervalos").
const maxBackoffMultiplier = 10

// maxConsecutiveTimeouts is how many hard-timeout violations in a row
// disable a collector (ADR-0007: "a la tercera desactiva el collector").
const maxConsecutiveTimeouts = 3

// Registration binds a Collector to its scheduling parameters.
type Registration struct {
	Collector Collector
	Interval  time.Duration
	Timeout   time.Duration
}

// Events receives runtime-level lifecycle notifications. The caller is
// responsible for turning these into Sink events if desired — the runtime
// doesn't assume a specific event schema.
type Events interface {
	CollectorDisabled(name, reason string)
	CollectorError(name string, err error)
	CollectorRecovered(name string)
}

// Runtime runs one goroutine per registered collector, each on its own
// ticker, enforcing a hard timeout, panic recovery and exponential backoff
// (ADR-0007).
type Runtime struct {
	Clock  Clock
	Sink   Sink
	Events Events

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// Start launches one goroutine per registration and returns immediately.
// Call Close to stop all of them and wait for their exit.
func (r *Runtime) Start(ctx context.Context, registrations []Registration) {
	if r.Clock == nil {
		r.Clock = SystemClock{}
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	for _, reg := range registrations {
		reg := reg
		// The ticker is created here, synchronously, so it's already
		// registered with the Clock before Start returns — a test driving
		// a fake Clock can't fire a tick before runLoop is listening for one.
		ticker := r.Clock.NewTicker(reg.Interval)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.runLoop(runCtx, reg, ticker)
		}()
	}
}

// Close stops every collector loop and waits for them to exit.
func (r *Runtime) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

func effectiveTimeout(interval, configured time.Duration) time.Duration {
	if hard := time.Duration(float64(interval) * 0.8); hard < configured {
		return hard
	}
	return configured
}

func (r *Runtime) runLoop(ctx context.Context, reg Registration, ticker Ticker) {
	interval := reg.Interval
	defer ticker.Stop()

	consecutiveErrors := 0
	consecutiveTimeouts := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			timeout := effectiveTimeout(interval, reg.Timeout)
			err, timedOut := r.collectOnce(ctx, reg.Collector, timeout)

			switch {
			case timedOut:
				consecutiveTimeouts++
				r.reportError(reg.Collector.Name(), fmt.Errorf("collect exceeded timeout %s", timeout))
				if consecutiveTimeouts >= maxConsecutiveTimeouts {
					r.reportDisabled(reg.Collector.Name(), "exceeded timeout 3 times in a row")
					return
				}

			case err != nil:
				consecutiveTimeouts = 0
				consecutiveErrors++
				r.reportError(reg.Collector.Name(), err)

				multiplier := 1
				for i := 0; i < consecutiveErrors; i++ {
					multiplier *= 2
					if multiplier >= maxBackoffMultiplier {
						multiplier = maxBackoffMultiplier
						break
					}
				}
				ticker.Stop()
				ticker = r.Clock.NewTicker(interval * time.Duration(multiplier))

			default:
				consecutiveTimeouts = 0
				if consecutiveErrors > 0 {
					consecutiveErrors = 0
					r.reportRecovered(reg.Collector.Name())
					ticker.Stop()
					ticker = r.Clock.NewTicker(interval)
				}
			}
		}
	}
}

// collectOnce runs one Collect call in its own goroutine so a collector
// that ignores context cancellation can't block the scheduler forever, and
// recovers any panic so it never crosses back into runLoop (ADR-0007: un
// pánico en un collector no tumba el agente).
func (r *Runtime) collectOnce(ctx context.Context, c Collector, timeout time.Duration) (err error, timedOut bool) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("panic in collector: %v", p)
			}
		}()
		done <- c.Collect(collectCtx, r.Sink)
	}()

	select {
	case err := <-done:
		return err, false
	case <-collectCtx.Done():
		return collectCtx.Err(), true
	}
}

func (r *Runtime) reportError(name string, err error) {
	if r.Events != nil {
		r.Events.CollectorError(name, err)
	}
}

func (r *Runtime) reportDisabled(name, reason string) {
	if r.Events != nil {
		r.Events.CollectorDisabled(name, reason)
	}
}

func (r *Runtime) reportRecovered(name string) {
	if r.Events != nil {
		r.Events.CollectorRecovered(name)
	}
}
