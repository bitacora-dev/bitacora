package hubapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/agentbuffer"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/collector/network"
	"github.com/bitacora-dev/bitacora/internal/hubapi"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// This file exercises the whole traffic path the panel actually depends
// on — the real network collector, the real collector runtime, the real
// agent Sink and buffer, the real metric store and the real summary
// handler — instead of hand-building samples that already agree on a
// timestamp. That assumption is precisely what broke: hubapi's own
// rateSeries tests passed the entire time the deployed panel read 0 B/s,
// because they wrote the same timestamp on every interface by hand while
// production wrote a different one per emission.

// steppingClock is a collector.Clock whose Now() advances a little on
// every read, the way a real clock does between two emissions, and whose
// tickers fire on demand.
type steppingClock struct {
	mu      sync.Mutex
	now     time.Time
	step    time.Duration
	tickers []*manualTicker
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(c.step)
	return c.now
}

func (c *steppingClock) NewTicker(time.Duration) collector.Ticker {
	t := &manualTicker{ch: make(chan time.Time, 1)}
	c.mu.Lock()
	c.tickers = append(c.tickers, t)
	c.mu.Unlock()
	return t
}

// advance moves the clock so that the NEXT Now() — the one the runtime
// takes to open a cycle — lands exactly d after the previous cycle's
// instant, despite the per-read step. Cycles are then exactly one interval
// apart and the expected rates are exact, while the step still guarantees
// two reads within a cycle would differ.
func (c *steppingClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d - c.step)
	tickers := append([]*manualTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, t := range tickers {
		select {
		case t.ch <- time.Time{}:
		default:
		}
	}
}

type manualTicker struct{ ch chan time.Time }

func (t *manualTicker) C() <-chan time.Time { return t.ch }
func (t *manualTicker) Stop()               {}

type noEvents struct{}

func (noEvents) ListEvents(context.Context, time.Time, time.Time, string) ([]schema.Event, error) {
	return nil, nil
}

func (noEvents) ListEventPage(context.Context, time.Time, time.Time, string, string, string, int, int) ([]schema.Event, int, error) {
	return nil, 0, nil
}

func procNetDev(counters map[string][2]uint64) string {
	b := strings.Builder{}
	b.WriteString("Inter-|   Receive                                                |  Transmit\n")
	b.WriteString(" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n")
	b.WriteString("    lo:  1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n")
	for iface, v := range counters {
		b.WriteString("  " + iface + ": " + strconv.FormatUint(v[0], 10) +
			" 1 0 0 0 0 0 0 " + strconv.FormatUint(v[1], 10) + " 1 0 0 0 0 0 0\n")
	}
	return b.String()
}

func writeProcNetDev(t *testing.T, path string, counters map[string][2]uint64) {
	t.Helper()
	if err := os.WriteFile(path, []byte(procNetDev(counters)), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// agentHarness wires the real agent path: collector runtime -> Sink ->
// buffer, with the counters file the test controls between cycles.
type agentHarness struct {
	clock      *steppingClock
	buffer     *agentbuffer.Buffer
	procNetDev string
	cycles     int
}

func newAgentHarness(t *testing.T, base time.Time, initial map[string][2]uint64) *agentHarness {
	t.Helper()
	dir := t.TempDir()
	procPath := filepath.Join(dir, "net_dev")
	writeProcNetDev(t, procPath, initial)

	buffer, err := agentbuffer.Open(filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	t.Cleanup(func() { buffer.Close() })

	clock := &steppingClock{now: base, step: time.Millisecond}
	sink := agentbuffer.NewSink("host-a", buffer, agentbuffer.WithClock(clock.Now))

	c := network.New()
	if err := c.Init(context.Background(), collector.Config{
		"proc_net_dev":  procPath,
		"spool_dir":     filepath.Join(dir, "vpn-spool"), // never created
		"sys_class_net": filepath.Join(dir, "no-sysfs"),  // absent: name fallback
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rt := &collector.Runtime{Clock: clock, Sink: sink}
	rt.Start(context.Background(), []collector.Registration{
		{Collector: c, Interval: 10 * time.Second, Timeout: 5 * time.Second},
	})
	t.Cleanup(rt.Close)

	return &agentHarness{clock: clock, buffer: buffer, procNetDev: procPath}
}

// runCycle fires one collection and waits until its metrics are in the
// buffer, so the test never races the runtime's collector goroutine.
func (h *agentHarness) runCycle(t *testing.T, wantNewItems int) {
	t.Helper()
	before := h.buffer.Len()
	h.clock.advance(10 * time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for h.buffer.Len() < before+wantNewItems {
		if time.Now().After(deadline) {
			t.Fatalf("cycle %d: expected %d new buffered items, got %d", h.cycles, wantNewItems, h.buffer.Len()-before)
		}
		time.Sleep(time.Millisecond)
	}
	h.cycles++
}

// drainIntoStore moves everything the agent buffered into a real metric
// store, exactly as the hub's ingest path would.
func (h *agentHarness) drainIntoStore(t *testing.T) *metricstore.Store {
	t.Helper()
	store, err := metricstore.Open(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error opening metric store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	send := func(ctx context.Context, items []agentbuffer.Item) error {
		for _, item := range items {
			if item.Metric == nil {
				continue
			}
			if err := store.Append(ctx, *item.Metric); err != nil {
				return err
			}
		}
		return nil
	}
	if err := h.buffer.Backfill(context.Background(), send, agentbuffer.BackfillOptions{}); err != nil {
		t.Fatalf("unexpected error draining buffer: %v", err)
	}
	return store
}

func fetchSummary(t *testing.T, store *metricstore.Store) hubapi.Summary {
	t.Helper()
	srv := &hubapi.Server{Metrics: store, Events: noEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a&window=1h", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var summary hubapi.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return summary
}

// TestNetworkPath_OneCycleAcrossInterfacesBecomesOneSummedPoint is the
// test that was missing. Several interfaces are emitted in a single
// collection cycle; if that cycle is split across timestamps, rateSeries
// produces one point per interface — each carrying a single interface's
// rate, and the most recent of them belonging to whichever interface
// sorted last — instead of one point carrying their sum.
func TestNetworkPath_OneCycleAcrossInterfacesBecomesOneSummedPoint(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond)
	h := newAgentHarness(t, base, map[string][2]uint64{
		"eno1":  {0, 0},
		"eno2":  {0, 0},
		"wlan0": {0, 0},
	})

	// Cycle 1: three interfaces, two counters each.
	h.runCycle(t, 6)

	// Cycle 2, ten seconds later: 300, 40 and 60 bytes received.
	writeProcNetDev(t, h.procNetDev, map[string][2]uint64{
		"eno1":  {3000, 6000},
		"eno2":  {400, 800},
		"wlan0": {600, 1200},
	})
	h.runCycle(t, 6)

	summary := fetchSummary(t, h.drainIntoStore(t))

	if len(summary.NetworkRXBytesPerSecond) != 1 {
		t.Fatalf("expected exactly one rate point for one collection cycle, got %d: %+v",
			len(summary.NetworkRXBytesPerSecond), summary.NetworkRXBytesPerSecond)
	}
	// (3000 + 400 + 600) / 10s
	if got := summary.NetworkRXBytesPerSecond[0].Value; got != 400 {
		t.Fatalf("expected the point to hold every interface's rate summed (400 B/s), got %v", got)
	}
	if len(summary.NetworkTXBytesPerSecond) != 1 {
		t.Fatalf("expected exactly one tx rate point, got %+v", summary.NetworkTXBytesPerSecond)
	}
	// (6000 + 800 + 1200) / 10s
	if got := summary.NetworkTXBytesPerSecond[0].Value; got != 800 {
		t.Fatalf("expected the tx point to sum to 800 B/s, got %v", got)
	}
}

// TestNetworkPath_IdleInterfaceDoesNotHideABusyOne is the reported
// symptom in its smallest form: an idle interface contributes 0 B/s. If
// each interface lands on its own timestamp, the panel's current-value
// readout shows whichever interface came last — and an idle one reads
// "0 B/s" on a host doing real work.
func TestNetworkPath_IdleInterfaceDoesNotHideABusyOne(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond)
	h := newAgentHarness(t, base, map[string][2]uint64{
		"eno1":  {1000, 1000},
		"wlan0": {500, 500}, // idle for the whole test
	})
	h.runCycle(t, 4)

	writeProcNetDev(t, h.procNetDev, map[string][2]uint64{
		"eno1":  {801000, 1291000}, // ~80 kB/s in, ~129 kB/s out over 10s
		"wlan0": {500, 500},
	})
	h.runCycle(t, 4)

	summary := fetchSummary(t, h.drainIntoStore(t))

	if len(summary.NetworkRXBytesPerSecond) != 1 {
		t.Fatalf("expected one rate point, got %+v", summary.NetworkRXBytesPerSecond)
	}
	latest := summary.NetworkRXBytesPerSecond[len(summary.NetworkRXBytesPerSecond)-1].Value
	if latest != 80000 {
		t.Fatalf("expected the current-value readout to be 80000 B/s, got %v", latest)
	}
	if latest == 0 {
		t.Fatal("the idle interface swallowed the busy one — this is the reported 0 B/s")
	}
}

// TestNetworkPath_InterfaceAppearingAndDisappearingProducesNoSpikeOrGap
// covers interfaces that come and go mid-window. Only device-backed
// interfaces are reported, so container veths never enter the series at
// all; but a real NIC coming up must still join without its whole
// cumulative counter being read as a one-cycle burst, and one going away
// must not leave the series empty.
func TestNetworkPath_InterfaceAppearingAndDisappearingProducesNoSpikeOrGap(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond)
	h := newAgentHarness(t, base, map[string][2]uint64{"eno1": {0, 0}})
	h.runCycle(t, 2)

	// eno2 appears carrying a large cumulative counter from before it was
	// visible. eno1 advances by 1000 bytes.
	writeProcNetDev(t, h.procNetDev, map[string][2]uint64{
		"eno1": {1000, 0},
		"eno2": {9_000_000, 0},
	})
	h.runCycle(t, 4)

	// Both advance by 1000.
	writeProcNetDev(t, h.procNetDev, map[string][2]uint64{
		"eno1": {2000, 0},
		"eno2": {9_001_000, 0},
	})
	h.runCycle(t, 4)

	// eno2 goes away. eno1 advances by 1000.
	writeProcNetDev(t, h.procNetDev, map[string][2]uint64{"eno1": {3000, 0}})
	h.runCycle(t, 2)

	points := fetchSummary(t, h.drainIntoStore(t)).NetworkRXBytesPerSecond
	if len(points) != 3 {
		t.Fatalf("expected one rate point per cycle after the first, got %d: %+v", len(points), points)
	}
	// Cycle 2: only eno1 has a predecessor, so 1000/10s. eno2's first
	// sample establishes its baseline and must contribute nothing.
	if points[0].Value != 100 {
		t.Fatalf("expected a newly appeared interface to contribute no spike (100 B/s), got %v", points[0].Value)
	}
	// Cycle 3: both advance 1000 bytes over 10s.
	if points[1].Value != 200 {
		t.Fatalf("expected both interfaces to contribute (200 B/s), got %v", points[1].Value)
	}
	// Cycle 4: eno2 is gone; eno1 alone still produces a point, not a gap.
	if points[2].Value != 100 {
		t.Fatalf("expected a disappearing interface to leave no gap (100 B/s), got %v", points[2].Value)
	}
}
