package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"

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

func (f *fakeEvents) ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error) {
	var out []schema.Event
	for _, e := range f.events {
		if e.HostID == hostID {
			out = append(out, e)
		}
	}
	return out, nil
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
