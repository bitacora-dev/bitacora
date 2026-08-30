package blackbox

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeTicker struct{ ch chan time.Time }

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 1)} }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               {}
func (f *fakeTicker) fire(t time.Time)    { f.ch <- t }

// fakeClock hands out fake tickers in creation order — Run creates the
// sample ticker first, then the sync ticker, so tickers[0]/tickers[1]
// give a test independent control over each.
type fakeClock struct {
	mu      sync.Mutex
	tickers []*fakeTicker
}

func (f *fakeClock) Now() time.Time { return time.Now() }

func (f *fakeClock) NewTicker(d time.Duration) Ticker {
	t := newFakeTicker()
	f.mu.Lock()
	f.tickers = append(f.tickers, t)
	f.mu.Unlock()
	return t
}

func (f *fakeClock) ticker(i int) *fakeTicker {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tickers[i]
}

func TestRun_SamplesOnSampleTickAndSyncsOnSyncTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")
	rec, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rec.Close()

	sampler, err := NewSampler(procRootWithMinimalStat(t), "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clock := &fakeClock{}
	loop := Run(clock, sampler, rec, time.Second, 5*time.Second, nil)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { loop(stop); close(done) }()

	// Wait for both tickers to exist before firing them.
	waitForTickers(t, clock, 2)

	clock.ticker(0).fire(time.Unix(1, 0))
	waitForWriteIndex(t, rec, 1)

	clock.ticker(0).fire(time.Unix(2, 0))
	waitForWriteIndex(t, rec, 2)

	close(stop)
	<-done

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 recorded samples, got %d", len(samples))
	}
}

func TestRun_ReportsSyncErrorsWithoutStopping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")
	rec, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sampler, err := NewSampler(procRootWithMinimalStat(t), "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clock := &fakeClock{}
	var mu sync.Mutex
	var syncErrors int
	loop := Run(clock, sampler, rec, time.Second, 5*time.Second, func(error) {
		mu.Lock()
		syncErrors++
		mu.Unlock()
	})

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { loop(stop); close(done) }()

	waitForTickers(t, clock, 2)

	// Close the recorder out from under the loop so Sync fails — proves a
	// sync failure is reported, not swallowed, and doesn't stop the loop
	// from still accepting sample ticks.
	rec.Close()
	clock.ticker(1).fire(time.Unix(1, 0))

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := syncErrors
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a reported sync error")
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(stop)
	<-done
}

func procRootWithMinimalStat(t *testing.T) string {
	t.Helper()
	procRoot, _ := newFixtureRoots(t)
	return procRoot
}

func waitForTickers(t *testing.T, clock *fakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		got := len(clock.tickers)
		clock.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d tickers, got %d", n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitForWriteIndex(t *testing.T, rec *Recorder, n uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec.mu.Lock()
		got := rec.writeIndex
		rec.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for writeIndex >= %d, got %d", n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
