package hubapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/transport"
)

func TestRevokeHostStopsTheAgentAndReportsWhatItRevoked(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	rec := post(t, srv, "/v1/hosts/host-a/revoke", deviceToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got RevokeHostResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.HostID != "host-a" || got.Revoked != 1 {
		t.Fatalf("response = %+v, want host-a with 1 revoked", got)
	}

	tokens, err := admin.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if !tokens[0].Revoked() {
		t.Fatal("the credential was not revoked in the store")
	}
}

// Revoking twice is what a retried request or a double-clicked button does.
func TestRevokeHostIsIdempotent(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	for call, want := range []int{1, 0} {
		rec := post(t, srv, "/v1/hosts/host-a/revoke", deviceToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var got RevokeHostResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if got.Revoked != want {
			t.Fatalf("call %d revoked %d, want %d", call, got.Revoked, want)
		}
	}
}

func TestRotateHostReturnsTheNewTokenOnceAndAGraceDeadline(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	rec := post(t, srv, "/v1/hosts/host-a/rotate", deviceToken, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got RotateHostResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Token == "" {
		t.Fatal("rotation returned no replacement token")
	}
	if !admin.verify("host-a", got.Token) {
		t.Fatal("the returned token does not verify against the stored hash")
	}
	if got.GraceExpiresAt == nil {
		t.Fatal("rotation reported no grace deadline, so an operator cannot know when the old token dies")
	}
	if got.TokenPath == "" {
		t.Fatalf("response = %+v, want the agent token path", got)
	}
}

// grace_seconds=0 is the compromised-token path: the old credential must be
// dead when the response is written, not in fifteen minutes.
func TestRotateHostWithoutGraceRevokesTheOldCredential(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	seeded, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "old-token")
	if err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	rec := post(t, srv, "/v1/hosts/host-a/rotate", deviceToken, `{"grace_seconds":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got RotateHostResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.GraceExpiresAt != nil {
		t.Fatalf("grace_expires_at = %v, want absent for an immediate cutover", got.GraceExpiresAt)
	}

	tokens, err := admin.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, token := range tokens {
		if token.ID == seeded.ID && !token.Revoked() {
			t.Fatal("the superseded credential is still live after an immediate rotation")
		}
	}
}

func TestRotateHostRejectsANegativeGrace(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("seeding token: %v", err)
	}
	rec := post(t, srv, "/v1/hosts/host-a/rotate", deviceToken, `{"grace_seconds":-60}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// A host that was never enrolled must look absent, not forbidden: ADR-0023
// refuses to confirm that someone else's server exists.
func TestRotateHostAnswers404ForAHostWithNoCredential(t *testing.T) {
	srv, _, _, deviceToken := newEnrollmentServer(t)
	rec := post(t, srv, "/v1/hosts/host-unknown/rotate", deviceToken, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHostMutationsRejectAnUnauthenticatedCaller(t *testing.T) {
	for _, path := range []string{"/v1/hosts/host-a/revoke", "/v1/hosts/host-a/rotate"} {
		srv, admin, _, _ := newEnrollmentServer(t)
		if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "token-a"); err != nil {
			t.Fatalf("seeding token: %v", err)
		}
		rec := post(t, srv, path, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}
}

// ADR-0023: a person who may only view a host may not cut off its telemetry,
// and the refusal looks like a missing host rather than a forbidden one.
func TestHostMutationsRefuseAHumanWithoutOperateOnThatHost(t *testing.T) {
	for _, path := range []string{"/v1/hosts/host-a/revoke", "/v1/hosts/host-a/rotate"} {
		srv, admin, _, _ := newEnrollmentServer(t)
		if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "token-a"); err != nil {
			t.Fatalf("seeding token: %v", err)
		}
		srv.Humans = fakeHumanIdentity{identity: localOperator}
		srv.HostScopes = assignedScopes{localOperator.Subject: {Hosts: map[string]hubauth.HostCapabilities{"host-a": {View: true}}}}

		rec := post(t, srv, path, "", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want %d (body %q)", path, rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}
}

func TestHostMutationsAnswer503WithoutATokenLifecycle(t *testing.T) {
	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	srv := &Server{Devices: devices, Hosts: newFakeTokenAdmin()}
	for _, path := range []string{"/v1/hosts/host-a/revoke", "/v1/hosts/host-a/rotate"} {
		rec := post(t, srv, path, deviceToken, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusServiceUnavailable)
		}
	}
}

func TestListHostsReportsCredentialStatus(t *testing.T) {
	now := time.Now().UTC()
	records := &fakeHostRecords{hosts: []schema.Host{
		{ID: "host-active", Name: "Active"},
		{ID: "host-revoked", Name: "Revoked"},
		{ID: "host-expired", Name: "Expired"},
		{ID: "host-bare", Name: "No credential"},
	}}
	admin := newFakeTokenAdmin()
	admin.tokens = []transport.TokenInfo{
		{ID: 1, HostID: "host-active", Label: "Active", CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute)},
		{ID: 2, HostID: "host-revoked", Label: "Revoked", CreatedAt: now.Add(-time.Hour), RevokedAt: now.Add(-time.Minute)},
		{ID: 3, HostID: "host-expired", Label: "Expired", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
	}

	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	srv := &Server{Devices: devices, HostRecords: records, Hosts: admin, Tokens: admin}

	entries := getHostList(t, srv, deviceToken)
	byID := make(map[string]HostListEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}

	for hostID, wantStatus := range map[string]string{
		"host-active":  HostStatusActive,
		"host-revoked": HostStatusRevoked,
		"host-expired": HostStatusExpired,
		"host-bare":    HostStatusNoCredential,
	} {
		entry, ok := byID[hostID]
		if !ok {
			t.Fatalf("host %s is missing from the listing", hostID)
		}
		if entry.Status != wantStatus {
			t.Fatalf("host %s status = %q, want %q", hostID, entry.Status, wantStatus)
		}
	}
	if !byID["host-revoked"].Revoked {
		t.Fatal("the revoked host does not report revoked=true")
	}
	if byID["host-active"].Revoked {
		t.Fatal("an active host reported revoked=true")
	}
	if byID["host-active"].CreatedAt == nil || byID["host-active"].TokenLastSeenAt == nil {
		t.Fatalf("active host = %+v, want created_at and token_last_seen_at", byID["host-active"])
	}
	if byID["host-bare"].CreatedAt != nil {
		t.Fatalf("a host with no credential reported created_at = %v", byID["host-bare"].CreatedAt)
	}
}

// A rotated host has more than one credential row. Its status has to come
// from the current one, not from the retired one.
func TestListHostsReportsTheNewestCredentialOfARotatedHost(t *testing.T) {
	now := time.Now().UTC()
	records := &fakeHostRecords{hosts: []schema.Host{{ID: "host-a", Name: "Rotated"}}}
	admin := newFakeTokenAdmin()
	admin.tokens = []transport.TokenInfo{
		{ID: 1, HostID: "host-a", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{ID: 2, HostID: "host-a", CreatedAt: now.Add(-time.Hour), RotatedFrom: 1},
	}

	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	srv := &Server{Devices: devices, HostRecords: records, Hosts: admin, Tokens: admin}

	entries := getHostList(t, srv, deviceToken)
	if len(entries) != 1 {
		t.Fatalf("listed %d hosts, want 1", len(entries))
	}
	if entries[0].Status != HostStatusActive {
		t.Fatalf("status = %q, want %q from the replacement credential", entries[0].Status, HostStatusActive)
	}
}

// The credential fields are additive: a hub with no lifecycle store wired
// must keep serving the array its existing clients parse.
func TestListHostsStaysBackwardCompatibleWithoutATokenLifecycle(t *testing.T) {
	records := &fakeHostRecords{hosts: []schema.Host{{ID: "host-a", Name: "Legacy", Hostname: "legacy.local"}}}
	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	srv := &Server{Devices: devices, HostRecords: records}

	entries := getHostList(t, srv, deviceToken)
	if len(entries) != 1 {
		t.Fatalf("listed %d hosts, want 1", len(entries))
	}
	if entries[0].ID != "host-a" || entries[0].Name != "Legacy" || entries[0].Hostname != "legacy.local" {
		t.Fatalf("entry = %+v, want the host record fields preserved", entries[0])
	}
	if entries[0].Status != HostStatusNoCredential {
		t.Fatalf("status = %q, want %q rather than a fabricated one", entries[0].Status, HostStatusNoCredential)
	}
}

func getHostList(t *testing.T, srv *Server, deviceToken string) []HostListEntry {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/hosts", nil)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	rec := serve(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var entries []HostListEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return entries
}

// A store failure must not be reported as a missing host: that sends an
// operator looking for a server that is right there.
func TestRotateHostReportsAStoreFailureAsSuch(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	if _, err := admin.AddLabeledToken(context.Background(), "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("seeding token: %v", err)
	}
	admin.rotatErr = errors.New("database is locked")

	rec := post(t, srv, "/v1/hosts/host-a/rotate", deviceToken, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

// A verified session with ADR-0023 operate no longer has to also present a
// device token to enroll a server — the same reasoning requireDeviceToken
// already applies to the read routes. The device-token path is unchanged,
// which the existing hosts_test cases cover.
func TestCreateHostAcceptsAHumanWithOperateWithoutADeviceToken(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)
	srv.Humans = fakeHumanIdentity{identity: localOperator}
	srv.HostScopes = assignedScopes{localOperator.Subject: {AllHosts: hubauth.HostCapabilities{View: true, Operate: true}}}

	rec := post(t, srv, "/v1/hosts", "", `{"name":"vps"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

// And a session without operate cannot, even though it can read the hub.
func TestCreateHostRefusesAHumanWithoutOperate(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)
	srv.Humans = fakeHumanIdentity{identity: localOperator}
	srv.HostScopes = assignedScopes{localOperator.Subject: {AllHosts: hubauth.HostCapabilities{View: true}}}

	rec := post(t, srv, "/v1/hosts", "", `{"name":"vps"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
