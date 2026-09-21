package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type expiredHuman struct{}

func (expiredHuman) HasSession(*http.Request) bool { return false }
func (expiredHuman) Identity(*http.Request) (hubauth.Identity, bool) {
	return hubauth.Identity{}, false
}
func (expiredHuman) SessionExpired(*http.Request) bool { return true }

type fakeMetrics struct {
	samples map[string][]metricstore.Sample // metric name -> samples, filtered by matchers
}

func (f *fakeMetrics) Query(ctx context.Context, name string, from, to time.Time, extra ...*labels.Matcher) ([]metricstore.Sample, error) {
	var out []metricstore.Sample
	for _, sample := range f.samples[name] {
		if matchesSample(name, sample, extra...) {
			out = append(out, sample)
		}
	}
	return out, nil
}

func matchesSample(name string, sample metricstore.Sample, matchers ...*labels.Matcher) bool {
	for _, matcher := range matchers {
		value := sample.Labels[matcher.Name]
		if matcher.Name == labels.MetricName {
			value = name
		}
		if !matcher.Matches(value) {
			return false
		}
	}
	return true
}

type fakeEvents struct {
	events []schema.Event
}

type fakeLogs struct{ entries []logstore.Entry }

type fakeJobPoller struct {
	job   schema.Job
	lines []schema.JobOutputLine
}

func (f *fakeJobPoller) GetJob(_ context.Context, hostID, jobID string) (schema.Job, bool, error) {
	return f.job, f.job.ID == jobID && f.job.HostID == hostID, nil
}

func (f *fakeJobPoller) ListJobOutput(_ context.Context, hostID, jobID string, after int64, limit int) ([]schema.JobOutputLine, int64, error) {
	var out []schema.JobOutputLine
	next := after
	for _, line := range f.lines {
		if f.job.HostID == hostID && line.JobID == jobID && line.Sequence > after && len(out) < limit {
			out = append(out, line)
			next = line.Sequence
		}
	}
	return out, next, nil
}

func (f *fakeLogs) Query(_ context.Context, q logstore.Query) (logstore.Page, error) {
	var matches []logstore.Entry
	for _, entry := range f.entries {
		if entry.HostID == q.HostID && !entry.TS.Before(q.From) && !entry.TS.After(q.To) && (q.Source == "" || entry.Source == q.Source) && (q.Unit == "" || entry.Unit == q.Unit) && (q.Text == "" || strings.Contains(entry.Message, q.Text)) && (q.BlockID == "" || strings.HasPrefix(entry.ID, q.BlockID+":")) {
			matches = append(matches, entry)
		}
	}
	if q.Offset >= len(matches) {
		return logstore.Page{Entries: []logstore.Entry{}, Total: len(matches)}, nil
	}
	end := min(q.Offset+q.Limit, len(matches))
	return logstore.Page{Entries: matches[q.Offset:end], Total: len(matches)}, nil
}

func TestLoginHandlerServesEmbeddedShellWithoutSession(t *testing.T) {
	srv := &Server{WebUI: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Bitácora</title>")}}, Humans: expiredHuman{}}
	rec := httptest.NewRecorder()
	srv.LoginHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("login shell status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Bitácora") {
		t.Fatalf("login shell did not return embedded UI: %q", rec.Body.String())
	}
}

func TestRequireHumanMarksAnExpiredSessionForTheLoginScreen(t *testing.T) {
	srv := &Server{Humans: expiredHuman{}}
	rec := httptest.NewRecorder()
	srv.requireHuman(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("expired session reached protected UI") })).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("expired session status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "expired=1") {
		t.Fatalf("login redirect = %q, want expiration marker", location)
	}
}

func (f *fakeEvents) ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error) {
	var out []schema.Event
	for _, e := range f.events {
		if e.HostID == hostID && !e.TS.Before(from) && !e.TS.After(to) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeEvents) ListEventPage(ctx context.Context, from, to time.Time, hostID, severity, eventType string, limit, offset int) ([]schema.Event, int, error) {
	var matches []schema.Event
	for _, e := range f.events {
		if e.HostID != hostID || e.TS.Before(from) || e.TS.After(to) || (severity != "" && string(e.Severity) != severity) || (eventType != "" && e.Type != eventType) {
			continue
		}
		matches = append(matches, e)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].TS.After(matches[j].TS) })
	total := len(matches)
	if offset >= total {
		return []schema.Event{}, total, nil
	}
	end := min(offset+limit, total)
	return matches[offset:end], total, nil
}

func TestHandleSummary_ReturnsCPUMemoryAndEvents(t *testing.T) {
	now := time.Now()
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_cpu_usage_ratio": {
			{Labels: map[string]string{"host_id": "host-a", "cpu": "total"}, Timestamp: now, Value: 0.42},
			{Labels: map[string]string{"host_id": "host-a", "cpu": "0"}, Timestamp: now, Value: 0.91},
			{Labels: map[string]string{"host_id": "host-a", "cpu": "1"}, Timestamp: now, Value: 0.13},
			{Labels: map[string]string{"host_id": "host-b", "cpu": "total"}, Timestamp: now, Value: 0.55},
		},
		"bitacora_memory_used_ratio":       {{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 0.7}},
		"bitacora_memory_total_bytes":      {{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 16 * 1024 * 1024 * 1024}},
		"bitacora_memory_available_bytes":  {{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 4 * 1024 * 1024 * 1024}},
		"bitacora_memory_swap_total_bytes": {{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 2 * 1024 * 1024 * 1024}},
		"bitacora_memory_swap_free_bytes":  {{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 1024 * 1024 * 1024}},
	}}
	events := &fakeEvents{events: []schema.Event{
		{ID: "evt-1", TS: now, HostID: "host-a", Source: "kernel", Type: "kernel.segfault", Severity: schema.SeverityError, Title: "segfault", Schema: 1},
	}}

	srv := &Server{Metrics: metrics, Events: events}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unexpected error decoding response: %v", err)
	}

	if got.HostID != "host-a" {
		t.Fatalf("expected host_id host-a, got %q", got.HostID)
	}
	if len(got.CPU) != 1 || got.CPU[0].Value != 0.42 {
		t.Fatalf("expected only the total cpu point at 0.42, got %+v", got.CPU)
	}
	if len(got.CPUCores) != 2 || got.CPUCores[0].CPU != "0" || got.CPUCores[1].CPU != "1" {
		t.Fatalf("expected two identified cpu core series, got %+v", got.CPUCores)
	}
	if len(got.Memory) != 1 || got.Memory[0].Value != 0.7 {
		t.Fatalf("expected 1 memory point at 0.7, got %+v", got.Memory)
	}
	if len(got.MemoryUsedBytes) != 1 || got.MemoryUsedBytes[0].Value != 12*1024*1024*1024 {
		t.Fatalf("expected 12 GiB memory used, got %+v", got.MemoryUsedBytes)
	}
	if len(got.MemoryTotalBytes) != 1 || got.MemoryTotalBytes[0].Value != 16*1024*1024*1024 {
		t.Fatalf("expected 16 GiB memory total, got %+v", got.MemoryTotalBytes)
	}
	if len(got.MemoryAvailableBytes) != 1 || got.MemoryAvailableBytes[0].Value != 4*1024*1024*1024 {
		t.Fatalf("expected 4 GiB memory available, got %+v", got.MemoryAvailableBytes)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "evt-1" {
		t.Fatalf("expected 1 event evt-1, got %+v", got.Events)
	}
}

func TestHandleSummary_KeepsCPUCoresAsSeparateSeries(t *testing.T) {
	now := time.Now()
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_cpu_usage_ratio": {
			{Labels: map[string]string{"host_id": "host-a", "cpu": "0"}, Timestamp: now, Value: 0.9},
			{Labels: map[string]string{"host_id": "host-a", "cpu": "total"}, Timestamp: now.Add(time.Second), Value: 0.42},
			{Labels: map[string]string{"host_id": "host-a", "cpu": "1"}, Timestamp: now.Add(2 * time.Second), Value: 0.2},
			{Labels: map[string]string{"host_id": "host-b", "cpu": "total"}, Timestamp: now.Add(3 * time.Second), Value: 0.77},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unexpected error decoding response: %v", err)
	}

	if len(got.CPU) != 1 {
		t.Fatalf("expected exactly 1 total cpu point, got %+v", got.CPU)
	}
	if got.CPU[0].Value != 0.42 || !got.CPU[0].TS.Equal(now.Add(time.Second)) {
		t.Fatalf("expected only host-a cpu=total point, got %+v", got.CPU[0])
	}
	if len(got.CPUCores) != 2 {
		t.Fatalf("expected two cpu core series, got %+v", got.CPUCores)
	}
	if got.CPUCores[0].CPU != "0" || len(got.CPUCores[0].Points) != 1 || got.CPUCores[0].Points[0].Value != 0.9 {
		t.Fatalf("expected cpu 0 to remain its own series, got %+v", got.CPUCores[0])
	}
	if got.CPUCores[1].CPU != "1" || len(got.CPUCores[1].Points) != 1 || got.CPUCores[1].Points[0].Value != 0.2 {
		t.Fatalf("expected cpu 1 to remain its own series, got %+v", got.CPUCores[1])
	}
}

func TestHandleSummary_KeepsTemperatureSensorsAsSeparateSeries(t *testing.T) {
	first := time.Now().Add(-time.Minute).UTC()
	second := first.Add(time.Minute)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_cpu_temperature_celsius": {
			{Labels: map[string]string{"host_id": "host-a", "chip": "coretemp", "sensor": "package_id_0"}, Timestamp: second, Value: 34},
			{Labels: map[string]string{"host_id": "host-a", "chip": "coretemp", "sensor": "core_0"}, Timestamp: first, Value: 31},
			{Labels: map[string]string{"host_id": "host-a", "chip": "coretemp", "sensor": "package_id_0"}, Timestamp: first, Value: 33},
			{Labels: map[string]string{"host_id": "host-a", "chip": "k10temp", "sensor": "tdie"}, Timestamp: first, Value: 42},
			{Labels: map[string]string{"host_id": "host-b", "chip": "coretemp", "sensor": "package_id_0"}, Timestamp: first, Value: 99},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Temperatures) != 3 {
		t.Fatalf("expected three identified temperature series, got %+v", got.Temperatures)
	}
	if got.Temperatures[0].Chip != "coretemp" || got.Temperatures[0].Sensor != "core_0" || len(got.Temperatures[0].Points) != 1 || got.Temperatures[0].Points[0].Value != 31 {
		t.Fatalf("expected coretemp core_0 to remain its own series, got %+v", got.Temperatures[0])
	}
	if got.Temperatures[1].Chip != "coretemp" || got.Temperatures[1].Sensor != "package_id_0" || len(got.Temperatures[1].Points) != 2 || got.Temperatures[1].Points[0].Value != 33 || got.Temperatures[1].Points[1].Value != 34 {
		t.Fatalf("expected coretemp package_id_0 to retain both ordered samples, got %+v", got.Temperatures[1])
	}
	if got.Temperatures[2].Chip != "k10temp" || got.Temperatures[2].Sensor != "tdie" || len(got.Temperatures[2].Points) != 1 || got.Temperatures[2].Points[0].Value != 42 {
		t.Fatalf("expected k10temp tdie to remain its own series, got %+v", got.Temperatures[2])
	}
}

func TestHandleSummary_OmitsTemperatureReadingsWhenNoSensorsExist(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{samples: map[string][]metricstore.Sample{}}, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Temperatures) != 0 {
		t.Fatalf("expected no fabricated temperature reading without sensors, got %+v", got.Temperatures)
	}
}

// TestHandleSummary_DerivesNetworkRateFromCumulativeCounters feeds the
// endpoint raw cumulative counters (what the network collector now emits)
// and asserts it returns a per-second rate: one host-level point per
// timestamp, summed across interfaces, with loopback excluded.
func TestHandleSummary_DerivesNetworkRateFromCumulativeCounters(t *testing.T) {
	first := time.Now().Add(-time.Second).UTC()
	second := first.Add(time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_net_rx_bytes_total": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: first, Value: 1000},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: first, Value: 500},
			{Labels: map[string]string{"host_id": "host-a", "interface": "lo"}, Timestamp: first, Value: 999999},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: second, Value: 1100},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: second, Value: 530},
		},
		"bitacora_net_tx_bytes_total": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: first, Value: 200},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: first, Value: 100},
			{Labels: map[string]string{"host_id": "host-a", "interface": "lo"}, Timestamp: first, Value: 999999},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: second, Value: 270},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: second, Value: 115},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// Only the second point yields a rate: rateSeries needs a predecessor
	// to differentiate against, so the first sample of each interface's
	// series produces no point at all (not a zero point).
	if len(got.NetworkRXBytesPerSecond) != 1 || got.NetworkRXBytesPerSecond[0].Value != 130 {
		t.Fatalf("expected one rx rate point summing both interfaces (100+30=130), got %+v", got.NetworkRXBytesPerSecond)
	}
	if len(got.NetworkTXBytesPerSecond) != 1 || got.NetworkTXBytesPerSecond[0].Value != 85 {
		t.Fatalf("expected one tx rate point summing both interfaces (70+15=85), got %+v", got.NetworkTXBytesPerSecond)
	}
}

// TestHandleSummary_NetworkRateSumsMultipleActiveInterfaces asserts the
// returned rate for a timestamp with several active interfaces is the sum
// of each interface's own rate, not a rate computed over their summed
// counters (that ordering is exactly what rateSeries's doc comment warns
// against).
func TestHandleSummary_NetworkRateSumsMultipleActiveInterfaces(t *testing.T) {
	t0 := time.Now().Add(-2 * time.Second).UTC()
	t1 := t0.Add(time.Second)
	t2 := t1.Add(time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_net_rx_bytes_total": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t0, Value: 0},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t1, Value: 100},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t2, Value: 200},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: t0, Value: 0},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: t1, Value: 40},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: t2, Value: 90},
		},
		"bitacora_net_tx_bytes_total": {},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.NetworkRXBytesPerSecond) != 2 {
		t.Fatalf("expected two rate points (t1, t2), got %+v", got.NetworkRXBytesPerSecond)
	}
	// t1: eth0 (0->100)/1s=100, wlan0 (0->40)/1s=40 => 140
	if got.NetworkRXBytesPerSecond[0].Value != 140 {
		t.Fatalf("expected first point to sum both interfaces' rates (100+40=140), got %v", got.NetworkRXBytesPerSecond[0].Value)
	}
	// t2: eth0 (100->200)/1s=100, wlan0 (40->90)/1s=50 => 150
	if got.NetworkRXBytesPerSecond[1].Value != 150 {
		t.Fatalf("expected second point to sum both interfaces' rates (100+50=150), got %v", got.NetworkRXBytesPerSecond[1].Value)
	}
}

// TestHandleSummary_NetworkRateSkipsCounterReset asserts a sample lower
// than its predecessor (a counter reset from a reboot or interface reset)
// is skipped rather than producing a negative or absurdly large rate.
func TestHandleSummary_NetworkRateSkipsCounterReset(t *testing.T) {
	t0 := time.Now().Add(-2 * time.Second).UTC()
	t1 := t0.Add(time.Second)
	t2 := t1.Add(time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_net_rx_bytes_total": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t0, Value: 5000},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t1, Value: 100}, // reset
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: t2, Value: 300},
		},
		"bitacora_net_tx_bytes_total": {},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// The t0->t1 pair is a reset and must be skipped entirely; only the
	// t1->t2 pair (100->300, 1s) survives, as a normal +200 B/s rate.
	if len(got.NetworkRXBytesPerSecond) != 1 {
		t.Fatalf("expected only the post-reset pair to produce a point, got %+v", got.NetworkRXBytesPerSecond)
	}
	if got.NetworkRXBytesPerSecond[0].Value != 200 {
		t.Fatalf("expected the surviving point to be a normal +200 B/s rate, got %v", got.NetworkRXBytesPerSecond[0].Value)
	}
	for _, p := range got.NetworkRXBytesPerSecond {
		if p.Value < 0 {
			t.Fatalf("expected no negative rate from a counter reset, got %+v", got.NetworkRXBytesPerSecond)
		}
	}
}

func TestHandleSummary_RequiresHostID(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing host_id, got %d", rec.Code)
	}
}

func TestHandleSummary_EmptyDataReturnsEmptyArraysNotNull(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{samples: map[string][]metricstore.Sample{}}, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, field := range []string{`"cpu":[]`, `"cpu_cores":[]`, `"memory":[]`, `"memory_total_bytes":[]`, `"memory_available_bytes":[]`, `"memory_used_bytes":[]`, `"events":[]`} {
		if !strings.Contains(body, field) {
			t.Fatalf("expected %s in response (empty array, not null), got %s", field, body)
		}
	}
}

func TestHandleSummary_DerivesMemoryUsedBytesFromTotalAndAvailable(t *testing.T) {
	now := time.Now()
	total := float64(8 * 1024 * 1024 * 1024)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_memory_total_bytes": {
			{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now.Add(-time.Second), Value: total},
		},
		"bitacora_memory_available_bytes": {
			{Labels: map[string]string{"host_id": "host-a"}, Timestamp: now, Value: 3 * 1024 * 1024 * 1024},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unexpected error decoding response: %v", err)
	}

	if len(got.MemoryUsedBytes) != 1 || got.MemoryUsedBytes[0].Value != 5*1024*1024*1024 {
		t.Fatalf("expected 5 GiB memory used derived from latest total, got %+v", got.MemoryUsedBytes)
	}
}

func TestHandleSummary_RejectsInvalidWindow(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a&window=not-a-duration", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid window, got %d", rec.Code)
	}
}

func TestHandleSummary_RejectsNonGET(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", rec.Code)
	}
}

func TestHandleSummary_RequiresDeviceTokenWhenDevicesConfigured(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: NewDeviceTokenStore()}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without an Authorization header, got %d", rec.Code)
	}
}

func TestHandleSummary_AcceptsValidDeviceToken(t *testing.T) {
	devices := NewDeviceTokenStore()
	_, token, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: devices}
	req := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with a valid device token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEventsHistory_FiltersAndPaginatesAuthenticatedRequests(t *testing.T) {
	devices := NewDeviceTokenStore()
	_, token, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("starting device token: %v", err)
	}
	from := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	events := &fakeEvents{events: []schema.Event{
		{ID: "old", TS: from.Add(-time.Minute), HostID: "host-a", Type: "kernel.segfault", Severity: schema.SeverityError},
		{ID: "first", TS: from.Add(time.Minute), HostID: "host-a", Type: "kernel.segfault", Severity: schema.SeverityError},
		{ID: "skip-severity", TS: from.Add(2 * time.Minute), HostID: "host-a", Type: "kernel.segfault", Severity: schema.SeverityInfo},
		{ID: "skip-type", TS: from.Add(3 * time.Minute), HostID: "host-a", Type: "service.restart", Severity: schema.SeverityError},
		{ID: "second", TS: from.Add(4 * time.Minute), HostID: "host-a", Type: "kernel.segfault", Severity: schema.SeverityError},
	}}
	srv := &Server{Metrics: &fakeMetrics{}, Events: events, Devices: devices}
	url := "/v1/events?host_id=host-a&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&severity=error&type=kernel.segfault&limit=1&offset=1"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got EventHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Total != 2 || got.Offset != 1 || got.Limit != 1 || len(got.Events) != 1 || got.Events[0].ID != "first" {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestHandleEventsHistory_RequiresExplicitRange(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/events?host_id=host-a", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing range, got %d", rec.Code)
	}
}

func TestHandleLogs_QueriesBoundedPage(t *testing.T) {
	from := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	srv := &Server{Logs: &fakeLogs{entries: []logstore.Entry{{ID: "one", TS: from.Add(time.Minute), HostID: "host-a", Source: "journald", Unit: "api.service", Message: "first"}, {ID: "two", TS: from.Add(2 * time.Minute), HostID: "host-a", Source: "journald", Unit: "api.service", Message: "second"}}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?host_id=host-a&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&source=journald&unit=api.service&limit=1&offset=1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got LogHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Entries) != 1 || got.Entries[0].ID != "two" {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestHandleLogs_BlockFilterReachesTheLinesALogRefNames(t *testing.T) {
	from := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	srv := &Server{Logs: &fakeLogs{entries: []logstore.Entry{
		{ID: "block-a:0", TS: from.Add(time.Minute), HostID: "host-a", Source: "journald", Message: "unrelated"},
		{ID: "block-b:0", TS: from.Add(2 * time.Minute), HostID: "host-a", Source: "journald", Message: "segfault preamble"},
		{ID: "block-b:1", TS: from.Add(3 * time.Minute), HostID: "host-a", Source: "journald", Message: "kernel: general protection fault"},
	}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?host_id=host-a&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&block=block-b&limit=50", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got LogHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 {
		t.Fatalf("expected only the referenced block, got %+v", got)
	}
	if got.Entries[1].ID != "block-b:1" {
		t.Fatalf("expected the referenced line to be identifiable by id, got %q", got.Entries[1].ID)
	}
}

func TestHandleLogs_EmptyAndInvalidRequests(t *testing.T) {
	srv := &Server{Logs: &fakeLogs{}}
	for _, tc := range []struct {
		name, url string
		want      int
	}{
		{"empty range", "/v1/logs?host_id=missing&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&limit=50&offset=0", http.StatusOK},
		{"missing host", "/v1/logs?from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z", http.StatusBadRequest},
		{"invalid limit", "/v1/logs?host_id=host-a&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&limit=501", http.StatusBadRequest},
		{"invalid offset", "/v1/logs?host_id=host-a&from=2026-01-02T10:00:00Z&to=2026-01-02T10:10:00Z&offset=-1", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
			if rec.Code != tc.want {
				t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleJobPoll_ReturnsSnapshotAndIncrementalOutput(t *testing.T) {
	started := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	poller := &fakeJobPoller{job: schema.Job{ID: "job-1", JobName: "backup", HostID: "host-a", StartedAt: started, Status: schema.JobRunning, Schema: 1}, lines: []schema.JobOutputLine{{JobID: "job-1", Sequence: 1, TS: started, Stream: "stdout", Message: "one"}, {JobID: "job-1", Sequence: 2, TS: started.Add(time.Second), Stream: "stderr", Message: "two"}}}
	srv := &Server{JobPoller: poller}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1?host_id=host-a&after=1&limit=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got JobPoll
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Job.ID != "job-1" || got.NextAfter != 2 || len(got.Lines) != 1 || got.Lines[0].Message != "two" || got.Complete {
		t.Fatalf("unexpected poll result: %+v", got)
	}
}

func TestHandleDevicePairAndClaim(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: NewDeviceTokenStore()}

	pairReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	pairRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pairRec, pairReq)

	if pairRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from pair, got %d: %s", pairRec.Code, pairRec.Body.String())
	}
	var pairResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(pairRec.Body.Bytes(), &pairResp); err != nil {
		t.Fatalf("unexpected error decoding pair response: %v", err)
	}
	if pairResp.Code == "" {
		t.Fatal("expected a non-empty pairing code")
	}

	claimReq := httptest.NewRequest(http.MethodPost, "/v1/devices/claim", strings.NewReader(`{"code":"`+pairResp.Code+`"}`))
	claimRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from claim, got %d: %s", claimRec.Code, claimRec.Body.String())
	}
	var claimResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("unexpected error decoding claim response: %v", err)
	}
	if claimResp.Token == "" {
		t.Fatal("expected a non-empty device token")
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil)
	summaryReq.Header.Set("Authorization", "Bearer "+claimResp.Token)
	summaryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(summaryRec, summaryReq)

	if summaryRec.Code != http.StatusOK {
		t.Fatalf("expected 200 using the claimed device token, got %d: %s", summaryRec.Code, summaryRec.Body.String())
	}
}

func TestHandleDevicePair_SecondPairingRequiresExistingDeviceToken(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: NewDeviceTokenStore()}

	// First pairing: unauthenticated, allowed — this is the bootstrap path.
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	firstRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected the first pairing to succeed unauthenticated, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	// Second pairing, no token at all: must be rejected — this is the
	// exact hole this test guards against (anyone minting themselves a
	// device token for free once the hub is reachable over a network).
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	secondRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected a second unauthenticated pairing to be rejected with 401, got %d: %s", secondRec.Code, secondRec.Body.String())
	}

	// Second pairing, garbage token: also rejected.
	garbageReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	garbageReq.Header.Set("Authorization", "Bearer not-a-real-token")
	garbageRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(garbageRec, garbageReq)
	if garbageRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected a pairing with an invalid device token to be rejected with 401, got %d: %s", garbageRec.Code, garbageRec.Body.String())
	}
}

func TestHandleDevicePair_SecondPairingSucceedsWithValidDeviceToken(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: NewDeviceTokenStore()}

	// Pair the first device end-to-end to get a real, usable device token.
	firstPairReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	firstPairRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstPairRec, firstPairReq)
	var firstPairResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(firstPairRec.Body.Bytes(), &firstPairResp); err != nil {
		t.Fatalf("unexpected error decoding first pair response: %v", err)
	}

	claimReq := httptest.NewRequest(http.MethodPost, "/v1/devices/claim", strings.NewReader(`{"code":"`+firstPairResp.Code+`"}`))
	claimRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(claimRec, claimReq)
	var claimResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("unexpected error decoding claim response: %v", err)
	}

	// A second, real device token in hand: pairing a second device must
	// now succeed.
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	secondReq.Header.Set("Authorization", "Bearer "+claimResp.Token)
	secondRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected pairing with a valid existing device token to succeed, got %d: %s", secondRec.Code, secondRec.Body.String())
	}
}

func TestHandleDeviceClaim_RejectsUnknownCode(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Devices: NewDeviceTokenStore()}

	req := httptest.NewRequest(http.MethodPost, "/v1/devices/claim", strings.NewReader(`{"code":"no-such-code"}`))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown pairing code, got %d", rec.Code)
	}
}

func TestHandleDevicePair_RespondsServiceUnavailableWithoutDevices(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}}

	req := httptest.NewRequest(http.MethodPost, "/v1/devices/pair", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Devices is nil, got %d", rec.Code)
	}
}

// TestHandleSummary_ExposesPublicSurfaceSignals is the regression test for
// the audit finding this endpoint shipped with: the public_surface
// collector wrote five metrics into tsdb and GET /v1/summary queried none
// of them, so a brute-force run against an internet-facing host was
// invisible on the dashboard while its evidence sat in the database.
func TestHandleSummary_ExposesPublicSurfaceSignals(t *testing.T) {
	first := time.Now().Add(-10 * time.Minute).UTC()
	second := first.Add(5 * time.Minute)
	host := map[string]string{"host_id": "host-a"}
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_public_ssh_failed_logins_total": {
			{Labels: host, Timestamp: first, Value: 400},
			{Labels: host, Timestamp: second, Value: 460},
		},
		"bitacora_public_fail2ban_jails_total": {
			{Labels: host, Timestamp: second, Value: 3},
		},
		"bitacora_public_fail2ban_banned_total": {
			{Labels: host, Timestamp: first, Value: 11},
			{Labels: host, Timestamp: second, Value: 14},
		},
		"bitacora_public_firewall_rules_total": {
			{Labels: host, Timestamp: second, Value: 27},
		},
		"bitacora_public_ovh_traffic_used_ratio": {
			{Labels: host, Timestamp: second, Value: 0.42},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a&window=1h", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	surface := got.PublicSurface

	if len(surface.SSHFailedLoginsTotal) != 2 || surface.SSHFailedLoginsTotal[1].Value != 460 {
		t.Fatalf("ssh failed login totals = %+v, want both raw samples", surface.SSHFailedLoginsTotal)
	}
	// 60 new failures over 300 seconds is 12 per minute. The first sample
	// has nothing to differentiate against, so it contributes no point.
	if len(surface.SSHFailedLoginsPerMinute) != 1 || surface.SSHFailedLoginsPerMinute[0].Value != 12 {
		t.Fatalf("ssh failed logins per minute = %+v, want a single 12/min point", surface.SSHFailedLoginsPerMinute)
	}
	if len(surface.Fail2BanJailsTotal) != 1 || surface.Fail2BanJailsTotal[0].Value != 3 {
		t.Fatalf("fail2ban jails = %+v, want 3", surface.Fail2BanJailsTotal)
	}
	if len(surface.Fail2BanBannedTotal) != 2 || surface.Fail2BanBannedTotal[1].Value != 14 {
		t.Fatalf("fail2ban banned = %+v, want 14 latest", surface.Fail2BanBannedTotal)
	}
	if len(surface.FirewallRulesTotal) != 1 || surface.FirewallRulesTotal[0].Value != 27 {
		t.Fatalf("firewall rules = %+v, want 27", surface.FirewallRulesTotal)
	}
	if len(surface.OVHTrafficUsedRatio) != 1 || surface.OVHTrafficUsedRatio[0].Value != 0.42 {
		t.Fatalf("ovh traffic ratio = %+v, want 0.42", surface.OVHTrafficUsedRatio)
	}
}

// TestHandleSummary_PublicSurfaceRateSkipsLogRotation guards the specific
// shape of this collector: it re-counts matching lines in the *current*
// auth log every cycle, so logrotate drops the counter back down. That drop
// must produce no point rather than a negative rate — a negative or zeroed
// "attempts per minute" reads as "the attack stopped", which is the exact
// class of false reassurance this panel exists to avoid.
func TestHandleSummary_PublicSurfaceRateSkipsLogRotation(t *testing.T) {
	first := time.Now().Add(-15 * time.Minute).UTC()
	second := first.Add(5 * time.Minute)
	third := second.Add(5 * time.Minute)
	host := map[string]string{"host_id": "host-a"}
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_public_ssh_failed_logins_total": {
			{Labels: host, Timestamp: first, Value: 900},
			{Labels: host, Timestamp: second, Value: 4}, // auth.log rotated
			{Labels: host, Timestamp: third, Value: 34},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a&window=1h", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	points := got.PublicSurface.SSHFailedLoginsPerMinute
	if len(points) != 1 || points[0].Value != 6 {
		t.Fatalf("per-minute points = %+v, want only the post-rotation interval at 6/min", points)
	}
	if !points[0].TS.Equal(third) {
		t.Fatalf("per-minute point timestamp = %s, want the post-rotation sample at %s", points[0].TS, third)
	}
}

// TestHandleSummary_PublicSurfaceAbsentIsEmptyNotZero is the "never an
// invented zero" contract. A host that is not operator-declared as publicly
// exposed runs no public_surface collector at all, so the honest answer is
// "nothing reported", expressed as empty arrays the UI can tell apart from
// a measured zero.
func TestHandleSummary_PublicSurfaceAbsentIsEmptyNotZero(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{samples: map[string][]metricstore.Sample{}}, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	body := rec.Body.String()
	for _, field := range []string{
		`"ssh_failed_logins_total":[]`,
		`"ssh_failed_logins_per_minute":[]`,
		`"fail2ban_jails_total":[]`,
		`"fail2ban_banned_total":[]`,
		`"firewall_rules_total":[]`,
		`"ovh_traffic_used_ratio":[]`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("expected %s in response (empty array, not null and not a zero point), got %s", field, body)
		}
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.PublicSurface.SSHFailedLoginsTotal) != 0 || len(got.PublicSurface.Fail2BanBannedTotal) != 0 {
		t.Fatalf("unreported public surface must carry no points, got %+v", got.PublicSurface)
	}
}

// TestHandleSummary_PublicSurfaceIsScopedToTheRequestedHost keeps the
// host_id matcher on every new query: a shared hub must never attribute one
// host's attack traffic to another.
func TestHandleSummary_PublicSurfaceIsScopedToTheRequestedHost(t *testing.T) {
	ts := time.Now().Add(-time.Minute).UTC()
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_public_fail2ban_banned_total": {
			{Labels: map[string]string{"host_id": "host-a"}, Timestamp: ts, Value: 7},
			{Labels: map[string]string{"host_id": "host-b"}, Timestamp: ts, Value: 91},
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.PublicSurface.Fail2BanBannedTotal) != 1 || got.PublicSurface.Fail2BanBannedTotal[0].Value != 7 {
		t.Fatalf("banned totals = %+v, want only host-a's 7", got.PublicSurface.Fail2BanBannedTotal)
	}
}

// containerSample builds one docker-collector sample: the collector always
// emits container_id truncated to 12 characters alongside container_name.
func containerSample(id, name string, ts time.Time, value float64) metricstore.Sample {
	return metricstore.Sample{Labels: map[string]string{"host_id": "host-a", "container_id": id, "container_name": name}, Timestamp: ts, Value: value}
}

// TestHandleSummary_KeepsContainersAsSeparateSeries is the guard against the
// mistake #699 made with logical CPUs: every container is its own series and
// flattening them produces one meaningless zig-zag line.
func TestHandleSummary_KeepsContainersAsSeparateSeries(t *testing.T) {
	t0 := time.Now().Add(-2 * time.Minute).UTC()
	t1 := t0.Add(30 * time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_container_cpu_seconds_total": {
			containerSample("aaaaaaaaaaaa", "dokploy-postgres", t0, 100),
			containerSample("aaaaaaaaaaaa", "dokploy-postgres", t1, 115),
			containerSample("bbbbbbbbbbbb", "dokploy-traefik", t0, 10),
			containerSample("bbbbbbbbbbbb", "dokploy-traefik", t1, 13),
		},
		"bitacora_container_memory_bytes": {
			containerSample("aaaaaaaaaaaa", "dokploy-postgres", t0, 512<<20),
			containerSample("aaaaaaaaaaaa", "dokploy-postgres", t1, 520<<20),
			containerSample("bbbbbbbbbbbb", "dokploy-traefik", t0, 64<<20),
			containerSample("bbbbbbbbbbbb", "dokploy-traefik", t1, 66<<20),
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("expected one series per container, got %+v", got.Containers)
	}
	postgres, traefik := got.Containers[0], got.Containers[1]
	if postgres.ContainerName != "dokploy-postgres" || traefik.ContainerName != "dokploy-traefik" {
		t.Fatalf("expected containers sorted by name, got %q and %q", postgres.ContainerName, traefik.ContainerName)
	}
	if postgres.ContainerID != "aaaaaaaaaaaa" || traefik.ContainerID != "bbbbbbbbbbbb" {
		t.Fatalf("expected each series to keep its own container id, got %q and %q", postgres.ContainerID, traefik.ContainerID)
	}

	// 15 CPU-seconds over 30s is half a core; 3 over 30s is a tenth.
	if len(postgres.CPUCoresUsed) != 1 || postgres.CPUCoresUsed[0].Value != 0.5 {
		t.Fatalf("expected postgres to use half a core on its own series, got %+v", postgres.CPUCoresUsed)
	}
	if len(traefik.CPUCoresUsed) != 1 || traefik.CPUCoresUsed[0].Value != 0.1 {
		t.Fatalf("expected traefik to use a tenth of a core on its own series, got %+v", traefik.CPUCoresUsed)
	}
	// A flattened implementation would report the pair's sum (0.6) on a single
	// series instead of keeping each container's own rate.
	if postgres.CPUCoresUsed[0].Value+traefik.CPUCoresUsed[0].Value != 0.6 {
		t.Fatalf("expected the two per-container rates to remain separate, got %+v and %+v", postgres.CPUCoresUsed, traefik.CPUCoresUsed)
	}

	if len(postgres.MemoryBytes) != 2 || postgres.MemoryBytes[0].Value != 512<<20 || postgres.MemoryBytes[1].Value != 520<<20 {
		t.Fatalf("expected postgres memory to stay on its own series in timestamp order, got %+v", postgres.MemoryBytes)
	}
	if len(traefik.MemoryBytes) != 2 || traefik.MemoryBytes[0].Value != 64<<20 {
		t.Fatalf("expected traefik memory to stay on its own series, got %+v", traefik.MemoryBytes)
	}
}

// TestHandleSummary_ContainerStartingMidWindowAddsNoSpuriousSpike is the guard
// against the mistake #885 made with network counters: summing cumulative
// counters across containers first and differentiating the sum afterwards
// turns every container start into a fake CPU spike. On a Dokploy host that
// happens all day long.
func TestHandleSummary_ContainerStartingMidWindowAddsNoSpuriousSpike(t *testing.T) {
	t0 := time.Now().Add(-90 * time.Second).UTC()
	t1 := t0.Add(30 * time.Second)
	t2 := t1.Add(30 * time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		// "old" has been running for hours, so its counter is already large.
		// "new" starts at t1 — a sum-then-differentiate pass would read old's
		// 3600 plus new's 0 as a 3600-CPU-second jump inside one interval.
		"bitacora_container_cpu_seconds_total": {
			containerSample("oldoldoldold", "long-running", t0, 3600),
			containerSample("oldoldoldold", "long-running", t1, 3603),
			containerSample("oldoldoldold", "long-running", t2, 3606),
			containerSample("newnewnewnew", "just-deployed", t1, 0),
			containerSample("newnewnewnew", "just-deployed", t2, 6),
		},
		"bitacora_container_memory_bytes": {},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("expected two containers, got %+v", got.Containers)
	}
	byName := map[string]ContainerSeries{}
	for _, container := range got.Containers {
		byName[container.ContainerName] = container
	}

	deployed := byName["just-deployed"]
	if len(deployed.CPUCoresUsed) != 1 {
		t.Fatalf("expected one rate point for the container that started mid-window, got %+v", deployed.CPUCoresUsed)
	}
	// Its first sample opens the series; it is a baseline, not a 0->6 jump
	// measured from nothing. 6 CPU-seconds over 30s is a fifth of a core.
	if deployed.CPUCoresUsed[0].Value != 0.2 {
		t.Fatalf("expected the new container's own rate (0.2 cores), got %v", deployed.CPUCoresUsed[0].Value)
	}

	running := byName["long-running"]
	if len(running.CPUCoresUsed) != 2 {
		t.Fatalf("expected two rate points for the long-running container, got %+v", running.CPUCoresUsed)
	}
	for _, point := range running.CPUCoresUsed {
		if point.Value != 0.1 {
			t.Fatalf("expected the long-running container to stay at 0.1 cores, unaffected by the new container; got %+v", running.CPUCoresUsed)
		}
	}
}

// TestHandleSummary_ContainerCPUSkipsCounterReset covers a container that is
// restarted in place: cgroup v2 starts its cpu.stat back at zero, which is a
// reset, not a negative rate.
func TestHandleSummary_ContainerCPUSkipsCounterReset(t *testing.T) {
	t0 := time.Now().Add(-90 * time.Second).UTC()
	t1 := t0.Add(30 * time.Second)
	t2 := t1.Add(30 * time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_container_cpu_seconds_total": {
			containerSample("cccccccccccc", "restarted", t0, 300),
			containerSample("cccccccccccc", "restarted", t1, 0),
			containerSample("cccccccccccc", "restarted", t2, 3),
		},
		"bitacora_container_memory_bytes": {},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 1 {
		t.Fatalf("expected one container, got %+v", got.Containers)
	}
	if len(got.Containers[0].CPUCoresUsed) != 1 || got.Containers[0].CPUCoresUsed[0].Value != 0.1 {
		t.Fatalf("expected the reset interval skipped and only the 0.1-core interval kept, got %+v", got.Containers[0].CPUCoresUsed)
	}
}

// TestHandleSummary_ContainerNameFollowsTheLatestSample covers the collector's
// ADR-0005 degraded mode: without docker-socket-proxy it falls back to the
// truncated ID as the name, and the real name only appears once the proxy
// answers again. The most recent label wins.
func TestHandleSummary_ContainerNameFollowsTheLatestSample(t *testing.T) {
	t0 := time.Now().Add(-60 * time.Second).UTC()
	t1 := t0.Add(30 * time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_container_cpu_seconds_total": {},
		// Same container id throughout; only the name label changes, because
		// the collector fell back to the truncated id until the proxy answered.
		"bitacora_container_memory_bytes": {
			containerSample("dddddddddddd", "dddddddddddd", t0, 1<<20),
			containerSample("dddddddddddd", "dokploy-redis", t1, 2<<20),
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 1 {
		t.Fatalf("expected the two samples to belong to one container, got %+v", got.Containers)
	}
	if got.Containers[0].ContainerName != "dokploy-redis" {
		t.Fatalf("expected the most recent container_name to win, got %q", got.Containers[0].ContainerName)
	}
}

// TestHandleSummary_HostWithoutContainersReportsAbsenceNotZero asserts a host
// running no containers answers with an empty list, never with zero-valued
// points a chart would draw as a flat "0 cores" line.
func TestHandleSummary_HostWithoutContainersReportsAbsenceNotZero(t *testing.T) {
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	if !strings.Contains(rec.Body.String(), `"containers":[]`) {
		t.Fatalf("expected an empty containers array rather than null, got %s", rec.Body.String())
	}
}

// TestHandleSummary_ContainersAreScopedToTheRequestedHost keeps another host's
// containers out of this host's panel.
func TestHandleSummary_ContainersAreScopedToTheRequestedHost(t *testing.T) {
	now := time.Now().UTC()
	other := containerSample("eeeeeeeeeeee", "elsewhere", now, 1<<20)
	other.Labels["host_id"] = "host-b"
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_container_memory_bytes": {
			containerSample("ffffffffffff", "here", now, 2<<20),
			other,
		},
	}}
	srv := &Server{Metrics: metrics, Events: &fakeEvents{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/summary?host_id=host-a", nil))

	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 1 || got.Containers[0].ContainerName != "here" {
		t.Fatalf("expected only host-a's container, got %+v", got.Containers)
	}
}
