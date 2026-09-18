package hubapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

type fakeHumanIdentity struct{ identity hubauth.Identity }

func (f fakeHumanIdentity) HasSession(*http.Request) bool { return f.identity.Subject != "" }
func (f fakeHumanIdentity) Identity(*http.Request) (hubauth.Identity, bool) {
	return f.identity, f.identity.Subject != ""
}

func TestPackageActionEndpointsRequireHumanIdentityBeforeIssuingOrConfirming(t *testing.T) {
	actions := actionconfirm.NewStore()
	srv := &Server{Actions: actions, Humans: fakeHumanIdentity{}}
	for _, path := range []string{"/v1/actions/package-operations/token", "/v1/actions/package-operations/confirm"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"host_id":"host-a","operation":"REFRESH_PACKAGE_CACHE"}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a human session returned %d, want 401", path, rec.Code)
		}
	}
	if order := actions.NextPendingOrder(t.Context(), "host-a"); order != nil {
		t.Fatalf("unauthenticated request created an order: %+v", order)
	}
}

func TestPackageActionsAreDisabledWithoutTheProductionActionStore(t *testing.T) {
	srv := &Server{Humans: fakeHumanIdentity{identity: hubauth.Identity{Subject: "human-a"}}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actions/package-operations/token", bytes.NewBufferString(`{"host_id":"host-a","operation":"REFRESH_PACKAGE_CACHE"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("default-disabled action endpoint returned %d, want 404", rec.Code)
	}
}

func TestPackageActionConfirmationUsesOnlyHumanBoundActionTokenAndClosedOrder(t *testing.T) {
	actions := actionconfirm.NewStore()
	srv := &Server{Actions: actions, Humans: fakeHumanIdentity{identity: hubauth.Identity{Subject: "human-a"}}}

	issue := httptest.NewRecorder()
	srv.Handler().ServeHTTP(issue, httptest.NewRequest(http.MethodPost, "/v1/actions/package-operations/token", bytes.NewBufferString(`{"host_id":"host-a","operation":"REFRESH_PACKAGE_CACHE"}`)))
	if issue.Code != http.StatusCreated {
		t.Fatalf("issuing action token returned %d: %s", issue.Code, issue.Body.String())
	}
	var issued struct {
		RequestID string `json:"request_id"`
		Token     string `json:"action_token"`
	}
	if err := json.NewDecoder(issue.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	confirmBody := `{"host_id":"host-a","operation":"REFRESH_PACKAGE_CACHE","request_id":"` + issued.RequestID + `","action_token":"` + issued.Token + `"}`
	confirm := httptest.NewRecorder()
	srv.Handler().ServeHTTP(confirm, httptest.NewRequest(http.MethodPost, "/v1/actions/package-operations/confirm", bytes.NewBufferString(confirmBody)))
	if confirm.Code != http.StatusAccepted {
		t.Fatalf("confirming action returned %d: %s", confirm.Code, confirm.Body.String())
	}
	order := actions.NextPendingOrder(t.Context(), "host-a")
	if order == nil || order.GetRequestId() != issued.RequestID || order.GetOperation() != bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE {
		t.Fatalf("confirmation did not produce the closed pending order: %+v", order)
	}
}

func TestPackageActionBarrierRejectsObservedDataAndFreeText(t *testing.T) {
	actions := actionconfirm.NewStore()
	srv := &Server{Actions: actions, Humans: fakeHumanIdentity{identity: hubauth.Identity{Subject: "human-a"}}}
	payload := `{"host_id":"host-a","operation":"REFRESH_PACKAGE_CACHE","inventory":"x","logs":"x","alerts":"x","tags":["x"],"text":"refresh","command":"apt update"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/actions/package-operations/token", bytes.NewBufferString(payload)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("observed/free-text payload returned %d: %s", rec.Code, rec.Body.String())
	}
	if order := actions.NextPendingOrder(t.Context(), "host-a"); order != nil {
		t.Fatalf("observed/free-text input created an order: %+v", order)
	}
}
