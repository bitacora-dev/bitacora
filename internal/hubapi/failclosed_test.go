package hubapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// setupBoundary is a boundary with no login source: the ADR-0025 fail-closed
// state of a fresh installation.
type setupBoundary struct{ required, available bool }

func (setupBoundary) HasSession(*http.Request) bool { return false }
func (setupBoundary) Identity(*http.Request) (hubauth.Identity, bool) {
	return hubauth.Identity{}, false
}
func (b setupBoundary) SetupRequired() bool  { return b.required }
func (b setupBoundary) SetupAvailable() bool { return b.available }

func TestSetupRequiredWithholdsTheDashboardAndEveryDataRoute(t *testing.T) {
	srv := &Server{
		Humans: setupBoundary{required: true, available: true},
		WebUI:  fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := srv.Handler()

	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/", http.StatusFound},
		{http.MethodGet, "/index.html", http.StatusFound},
		{http.MethodGet, "/v1/summary?host_id=h", http.StatusServiceUnavailable},
		{http.MethodGet, "/v1/logs", http.StatusServiceUnavailable},
		{http.MethodGet, "/v1/inventory", http.StatusServiceUnavailable},
		{http.MethodGet, "/v1/jobs/x", http.StatusServiceUnavailable},
		{http.MethodGet, "/v1/hosts", http.StatusServiceUnavailable},
		{http.MethodPost, "/v1/devices/pair", http.StatusServiceUnavailable},
		{http.MethodPost, "/v1/devices/claim", http.StatusServiceUnavailable},
		{http.MethodPost, "/v1/actions/package-operations/confirm", http.StatusServiceUnavailable},
		{http.MethodPost, "/", http.StatusServiceUnavailable},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer device-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.status)
		}
		if tc.status == http.StatusFound && rec.Header().Get("Location") != "/auth/setup" {
			t.Errorf("%s %s redirected to %q, want /auth/setup", tc.method, tc.path, rec.Header().Get("Location"))
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	var body authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !body.AuthEnabled || !body.SetupRequired || !body.SetupAvailable {
		t.Fatalf("/v1/auth/session = %d %+v; want setup required and available", rec.Code, body)
	}
}

// Once a login source exists the gate disappears and the ordinary boundary
// takes over.
func TestSetupGateStepsAsideOnceALoginSourceExists(t *testing.T) {
	srv := &Server{
		Humans: setupBoundary{},
		WebUI:  fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") == "/auth/setup" {
		t.Fatalf("GET / = %d %q; want the ordinary login redirect", rec.Code, rec.Header().Get("Location"))
	}
}

// On a configured hub a pending recovery token is not advertised.
func TestSessionHidesAPendingTokenWhenSetupIsNotRequired(t *testing.T) {
	srv := &Server{Humans: setupBoundary{available: true}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	var body authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SetupRequired || body.SetupAvailable {
		t.Fatalf("/v1/auth/session = %+v; want no setup advertised", body)
	}
}
