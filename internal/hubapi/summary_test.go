package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

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

func (f *fakeLogs) Query(_ context.Context, q logstore.Query) (logstore.Page, error) {
	var matches []logstore.Entry
	for _, entry := range f.entries {
		if entry.HostID == q.HostID && !entry.TS.Before(q.From) && !entry.TS.After(q.To) && (q.Source == "" || entry.Source == q.Source) && (q.Unit == "" || entry.Unit == q.Unit) && (q.Text == "" || strings.Contains(entry.Message, q.Text)) {
			matches = append(matches, entry)
		}
	}
	if q.Offset >= len(matches) {
		return logstore.Page{Entries: []logstore.Entry{}, Total: len(matches)}, nil
	}
	end := min(q.Offset+q.Limit, len(matches))
	return logstore.Page{Entries: matches[q.Offset:end], Total: len(matches)}, nil
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

func TestHandleSummary_FiltersCPUToTotalSeries(t *testing.T) {
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
}

func TestHandleSummary_AggregatesNetworkInterfacesAndExcludesLoopback(t *testing.T) {
	first := time.Now().Add(-time.Second).UTC()
	second := first.Add(time.Second)
	metrics := &fakeMetrics{samples: map[string][]metricstore.Sample{
		"bitacora_net_rx_bytes_per_second": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: first, Value: 100},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: first, Value: 25},
			{Labels: map[string]string{"host_id": "host-a", "interface": "lo"}, Timestamp: first, Value: 999},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: second, Value: 140},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: second, Value: 30},
		},
		"bitacora_net_tx_bytes_per_second": {
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: first, Value: 50},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: first, Value: 10},
			{Labels: map[string]string{"host_id": "host-a", "interface": "lo"}, Timestamp: first, Value: 999},
			{Labels: map[string]string{"host_id": "host-a", "interface": "eth0"}, Timestamp: second, Value: 70},
			{Labels: map[string]string{"host_id": "host-a", "interface": "wlan0"}, Timestamp: second, Value: 15},
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
	if len(got.NetworkRXBytesPerSecond) != 2 || got.NetworkRXBytesPerSecond[0].Value != 125 || got.NetworkRXBytesPerSecond[1].Value != 170 {
		t.Fatalf("expected two aggregated receive points without loopback, got %+v", got.NetworkRXBytesPerSecond)
	}
	if len(got.NetworkTXBytesPerSecond) != 2 || got.NetworkTXBytesPerSecond[0].Value != 60 || got.NetworkTXBytesPerSecond[1].Value != 85 {
		t.Fatalf("expected two aggregated transmit points without loopback, got %+v", got.NetworkTXBytesPerSecond)
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
	for _, field := range []string{`"cpu":[]`, `"memory":[]`, `"memory_total_bytes":[]`, `"memory_available_bytes":[]`, `"memory_used_bytes":[]`, `"events":[]`} {
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
