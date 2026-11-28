package hubapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

type fakeHumanBoundary struct {
	identity hubauth.Identity
	local    bool
	oidc     bool
	pending  bool
}

func (f fakeHumanBoundary) HasSession(*http.Request) bool { return f.identity.Subject != "" }
func (f fakeHumanBoundary) Identity(*http.Request) (hubauth.Identity, bool) {
	return f.identity, f.identity.Subject != ""
}
func (f fakeHumanBoundary) LoginSources() (bool, bool)       { return f.local, f.oidc }
func (f fakeHumanBoundary) LocalPendingInitialization() bool { return f.pending }

func getAuthSession(t *testing.T, srv *Server) authSessionResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth/session status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body authSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return body
}

// The route an operator curls to find out what the hub's human boundary
// actually is. Answering 404 there — as the deployment did — is
// indistinguishable from "this build has no authentication at all".
func TestAuthSessionReportsADisabledBoundaryWithoutA404(t *testing.T) {
	body := getAuthSession(t, &Server{})
	if body.AuthEnabled || body.Authenticated || body.LocalEnabled || body.OIDCEnabled {
		t.Errorf("response = %+v; want everything off for a hub without authentication", body)
	}
}

func TestAuthSessionReportsLocalAuthenticationPendingInitialization(t *testing.T) {
	body := getAuthSession(t, &Server{Humans: fakeHumanBoundary{pending: true}})
	if !body.AuthEnabled {
		t.Error("auth_enabled = false while the boundary is wired")
	}
	if body.LocalEnabled {
		t.Error("local_enabled = true with no credential to accept")
	}
	if !body.LocalPendingInitialization {
		t.Error("local_pending_initialization = false, want true")
	}
	if body.Authenticated {
		t.Error("authenticated = true without a session")
	}
}

func TestAuthSessionReportsAnEnabledLocalSource(t *testing.T) {
	body := getAuthSession(t, &Server{Humans: fakeHumanBoundary{local: true}})
	if !body.LocalEnabled || body.LocalPendingInitialization || body.Authenticated {
		t.Errorf("response = %+v; want an enabled, initialized local source and no session", body)
	}
}

func TestAuthSessionReportsTheSignedInIdentity(t *testing.T) {
	body := getAuthSession(t, &Server{Humans: fakeHumanBoundary{
		identity: hubauth.Identity{Source: "local", Subject: "local:operator", Name: "Local operator"},
		local:    true,
	}})
	if !body.Authenticated || body.Identity == nil || body.Identity.Subject != "local:operator" {
		t.Fatalf("response = %+v; want the signed-in local operator", body)
	}
}

func TestAuthSessionRejectsNonGETMethods(t *testing.T) {
	srv := &Server{Humans: fakeHumanBoundary{local: true}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
