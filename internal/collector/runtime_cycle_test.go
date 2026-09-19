package collector

import (
	"context"
	"sync"
	"testing"
	"time"
)

// cycleRecordingSink implements CycleSink. Emissions on the value returned
// by BeginCycle are recorded against that cycle's instant; emissions on the
// shared receiver fall back to the clock, which is the pre-fix behaviour.
type cycleRecordingSink struct {
	clock *steppingClock

	mu     sync.Mutex
	cycles [][]time.Time
}

func (s *cycleRecordingSink) BeginCycle(now time.Time) Sink {
	s.mu.Lock()
	s.cycles = append(s.cycles, nil)
	index := len(s.cycles) - 1
	s.mu.Unlock()
	return &boundCycle{parent: s, index: index, at: now}
}

// Emissions straight on the receiver land in no cycle at all, which is how
// this sink reports "the runtime never opened one".
func (s *cycleRecordingSink) Gauge(string, float64, Labels)   { s.append(-1, s.clock.Now()) }
func (s *cycleRecordingSink) Counter(string, float64, Labels) { s.append(-1, s.clock.Now()) }
func (s *cycleRecordingSink) Event(Event)                     {}
func (s *cycleRecordingSink) LogLines(string, []LogLine)      {}
func (s *cycleRecordingSink) Inventory(Inventory)             {}

func (s *cycleRecordingSink) append(index int, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 {
		s.cycles = append(s.cycles, []time.Time{at})
		return
	}
	s.cycles[index] = append(s.cycles[index], at)
}

type boundCycle struct {
	parent *cycleRecordingSink
	index  int
	at     time.Time
}

func (b *boundCycle) Gauge(string, float64, Labels)   { b.parent.append(b.index, b.at) }
func (b *boundCycle) Counter(string, float64, Labels) { b.parent.append(b.index, b.at) }
func (b *boundCycle) Event(Event)                     {}
func (b *boundCycle) LogLines(string, []LogLine)      {}
func (b *boundCycle) Inventory(Inventory)             {}

// steppingClock advances on every Now() call, so a runtime reading the
// clock per emission instead of per cycle produces visibly different
// stamps within one cycle.
type steppingClock struct {
	fakeClock
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(c.step)
	return c.now
}

// emittingCollector emits a fixed number of metrics through whichever Sink
// the runtime hands it, then signals that the cycle is done.
type emittingCollector struct {
	emissions int
	done      chan struct{}
}

func (c *emittingCollector) Name() string                                  { return "emitting" }
func (c *emittingCollector) Requires() []Capability                        { return nil }
func (c *emittingCollector) Init(context.Context, Config, *HostInfo) error { return nil }
func (c *emittingCollector) Close() error                                  { return nil }

func (c *emittingCollector) Collect(_ context.Context, sink Sink) error {
	for i := 0; i < c.emissions; i++ {
		sink.Counter("bitacora_net_rx_bytes_total", float64(i), Labels{"interface": "eth" + string(rune('0'+i))})
	}
	c.done <- struct{}{}
	return nil
}

// TestRuntime_GivesOneCollectCallOneInstant asserts the runtime opens a
// cycle on a CycleSink and hands that cycle to Collect. Without it every
// emission carried its own clock reading, which is what split one network
// collection across as many timestamps as the host had interfaces and left
// the traffic panel summing one interface at a time.
func TestRuntime_GivesOneCollectCallOneInstant(t *testing.T) {
	clock := &steppingClock{now: time.Unix(1_700_000_000, 0), step: time.Millisecond}
	sink := &cycleRecordingSink{clock: clock}
	c := &emittingCollector{emissions: 4, done: make(chan struct{}, 1)}

	rt := Runtime{Clock: clock, Sink: sink}
	rt.Start(context.Background(), []Registration{{Collector: c, Interval: time.Second, Timeout: time.Second}})
	defer rt.Close()

	clock.fakeClock.tick()
	<-c.done
	clock.fakeClock.tick()
	<-c.done

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.cycles) != 2 {
		t.Fatalf("expected 2 cycles, got %d: %+v", len(sink.cycles), sink.cycles)
	}
	for i, cycle := range sink.cycles {
		if len(cycle) != c.emissions {
			t.Fatalf("cycle %d: expected %d emissions, got %d", i, c.emissions, len(cycle))
		}
		for _, at := range cycle {
			if !at.Equal(cycle[0]) {
				t.Fatalf("cycle %d: expected every emission at %s, got %s", i, cycle[0], at)
			}
		}
	}
	if sink.cycles[0][0].Equal(sink.cycles[1][0]) {
		t.Fatalf("expected consecutive cycles to carry different instants, both were %s", sink.cycles[0][0])
	}
}

// TestRuntime_PlainSinkIsPassedThroughUnchanged asserts CycleSink stays a
// capability: a Sink that doesn't implement it still receives every
// emission directly, so existing collectors and test doubles keep working.
func TestRuntime_PlainSinkIsPassedThroughUnchanged(t *testing.T) {
	clock := &steppingClock{now: time.Unix(1_700_000_000, 0), step: time.Millisecond}
	sink := &countingSink{}
	c := &emittingCollector{emissions: 3, done: make(chan struct{}, 1)}

	rt := Runtime{Clock: clock, Sink: sink}
	rt.Start(context.Background(), []Registration{{Collector: c, Interval: time.Second, Timeout: time.Second}})
	defer rt.Close()

	clock.fakeClock.tick()
	<-c.done

	if got := sink.count(); got != 3 {
		t.Fatalf("expected the plain sink to receive 3 emissions, got %d", got)
	}
}

type countingSink struct {
	mu sync.Mutex
	n  int
}

func (s *countingSink) Gauge(string, float64, Labels) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}
func (s *countingSink) Counter(n string, v float64, l Labels) { s.Gauge(n, v, l) }
func (s *countingSink) Event(Event)                           {}
func (s *countingSink) LogLines(string, []LogLine)            {}
func (s *countingSink) Inventory(Inventory)                   {}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}
