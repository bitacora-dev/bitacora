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
	samples map[string][]metricstore.Sample // metric name -> samples, ignores matchers
}

func (f *fakeMetrics) Query(ctx context.Context, name string, from, to time.Time, extra ...*labels.Matcher) ([]metricstore.Sample, error) {
	return f.samples[name], nil
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
		"bitacora_cpu_usage_ratio":   {{Timestamp: now, Value: 0.42}},
		"bitacora_memory_used_ratio": {{Timestamp: now, Value: 0.7}},
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
		t.Fatalf("expected 1 cpu point at 0.42, got %+v", got.CPU)
	}
	if len(got.Memory) != 1 || got.Memory[0].Value != 0.7 {
		t.Fatalf("expected 1 memory point at 0.7, got %+v", got.Memory)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "evt-1" {
		t.Fatalf("expected 1 event evt-1, got %+v", got.Events)
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
	for _, field := range []string{`"cpu":[]`, `"memory":[]`, `"events":[]`} {
		if !strings.Contains(body, field) {
			t.Fatalf("expected %s in response (empty array, not null), got %s", field, body)
		}
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
