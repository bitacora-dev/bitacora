package collector

import (
	"context"
	"errors"
	goruntime "runtime"
	"sync"
	"testing"
	"time"
)

// fakeTicker lets a test fire ticks on demand instead of waiting on real time.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 1)} }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               {}

// fakeClock hands out fakeTickers and remembers every one it created, so a
// test can advance a collector's schedule deterministically.
type fakeClock struct {
	mu      sync.Mutex
	tickers []*fakeTicker
}

func (f *fakeClock) Now() time.Time { return time.Time{} }

func (f *fakeClock) NewTicker(d time.Duration) Ticker {
	t := newFakeTicker()
	f.mu.Lock()
	f.tickers = append(f.tickers, t)
	f.mu.Unlock()
	return t
}

// tick fires every ticker ever created. Only the one currently being read
// by runLoop has any effect; sends are non-blocking so retired tickers
// never stall this call.
func (f *fakeClock) tick() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tickers {
		select {
		case t.ch <- time.Now():
		default:
		}
	}
}

// scriptedCollector lets each test control exactly what Collect does.
type scriptedCollector struct {
	name    string
	collect func(ctx context.Context) error
}

func (c *scriptedCollector) Name() string                                  { return c.name }
func (c *scriptedCollector) Requires() []Capability                        { return nil }
func (c *scriptedCollector) Init(context.Context, Config, *HostInfo) error { return nil }
func (c *scriptedCollector) Close() error                                  { return nil }
func (c *scriptedCollector) Collect(ctx context.Context, sink Sink) error {
	return c.collect(ctx)
}

type noopSink struct{}

func (noopSink) Gauge(string, float64, Labels)   {}
func (noopSink) Counter(string, float64, Labels) {}
func (noopSink) Event(Event)                     {}
func (noopSink) LogLines(string, []LogLine)      {}

type recordingEvents struct {
	mu        sync.Mutex
	errors    []string
	disabled  []string
	recovered []string
}

func (r *recordingEvents) CollectorError(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, name)
}

func (r *recordingEvents) CollectorDisabled(name, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = append(r.disabled, name)
}

func (r *recordingEvents) CollectorRecovered(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recovered = append(r.recovered, name)
}

func (r *recordingEvents) counts() (errs, disabled, recovered int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.errors), len(r.disabled), len(r.recovered)
}

func TestRuntime_HardTimeoutDisablesAfterThreeStrikes(t *testing.T) {
	clock := &fakeClock{}
	events := &recordingEvents{}
	unblock := make(chan struct{})
	defer close(unblock)

	c := &scriptedCollector{
		name: "stuck",
		collect: func(ctx context.Context) error {
			// Ignores ctx cancellation on purpose: this is exactly the
			// misbehaving collector ADR-0007 says the hard timeout must
			// contain.
			<-unblock
			return nil
		},
	}

	rt := &Runtime{Clock: clock, Sink: noopSink{}, Events: events}
	rt.Start(context.Background(), []Registration{{
		Collector: c,
		Interval:  50 * time.Millisecond,
		Timeout:   10 * time.Millisecond,
	}})
	defer rt.Close()

	for i := 0; i < 3; i++ {
		clock.tick()
		time.Sleep(30 * time.Millisecond)
	}

	errs, disabled, _ := events.counts()
	if errs < 3 {
		t.Fatalf("expected at least 3 timeout errors reported, got %d", errs)
	}
	if disabled != 1 {
		t.Fatalf("expected collector to be disabled once after 3 timeouts, got %d disables", disabled)
	}
}

func TestRuntime_PanicIsRecoveredAndReported(t *testing.T) {
	clock := &fakeClock{}
	events := &recordingEvents{}

	c := &scriptedCollector{
		name: "panicky",
		collect: func(ctx context.Context) error {
			panic("boom")
		},
	}

	rt := &Runtime{Clock: clock, Sink: noopSink{}, Events: events}
	rt.Start(context.Background(), []Registration{{
		Collector: c,
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
	}})
	defer rt.Close()

	clock.tick()
	time.Sleep(30 * time.Millisecond)

	errs, _, _ := events.counts()
	if errs != 1 {
		t.Fatalf("expected exactly 1 reported error from the panic, got %d", errs)
	}
}

func TestRuntime_BackoffThenRecovery(t *testing.T) {
	clock := &fakeClock{}
	events := &recordingEvents{}

	failing := true
	c := &scriptedCollector{
		name: "flaky",
		collect: func(ctx context.Context) error {
			if failing {
				return errors.New("boom")
			}
			return nil
		},
	}

	rt := &Runtime{Clock: clock, Sink: noopSink{}, Events: events}
	rt.Start(context.Background(), []Registration{{
		Collector: c,
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
	}})
	defer rt.Close()

	// Two failures trigger backoff (new tickers get created internally).
	clock.tick()
	time.Sleep(20 * time.Millisecond)
	clock.tick()
	time.Sleep(20 * time.Millisecond)

	errs, _, recovered := events.counts()
	if errs != 2 {
		t.Fatalf("expected 2 errors before recovery, got %d", errs)
	}
	if recovered != 0 {
		t.Fatalf("expected no recovery yet, got %d", recovered)
	}

	failing = false
	clock.tick()
	time.Sleep(20 * time.Millisecond)

	_, _, recovered = events.counts()
	if recovered != 1 {
		t.Fatalf("expected exactly 1 recovery event, got %d", recovered)
	}
}

func TestRuntime_CloseLeavesNoGoroutineLeak(t *testing.T) {
	clock := &fakeClock{}
	c := &scriptedCollector{
		name: "well-behaved",
		collect: func(ctx context.Context) error {
			return nil
		},
	}

	before := goruntime.NumGoroutine()

	rt := &Runtime{Clock: clock, Sink: noopSink{}}
	rt.Start(context.Background(), []Registration{{
		Collector: c,
		Interval:  10 * time.Millisecond,
		Timeout:   10 * time.Millisecond,
	}})

	clock.tick()
	time.Sleep(20 * time.Millisecond)

	rt.Close()

	// Scheduling goroutines can take a moment to unwind; poll briefly
	// instead of asserting instantly.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		after := goruntime.NumGoroutine()
		if after <= before+1 { // small slack for the Go runtime's own bookkeeping
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak after Close(): before=%d after=%d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
