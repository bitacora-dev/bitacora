package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/transport"
)

// fakeRegistrar records what POST /v1/hosts asked to register, and can be
// queried the same way the real store is (Argon2id verify), so a test can
// assert the plaintext token really matches the persisted hash.
type fakeRegistrar struct {
	mu      sync.Mutex
	entries []struct{ hostID, hash string }
	err     error
}

type fakeHostRecords struct {
	created []struct{ id, name string }
	hosts   []schema.Host
}

func (f *fakeHostRecords) CreateHost(_ context.Context, hostID, name string) error {
	f.created = append(f.created, struct{ id, name string }{hostID, name})
	return nil
}

func (f *fakeHostRecords) ListHosts(context.Context) ([]schema.Host, error) { return f.hosts, nil }

func (f *fakeRegistrar) AddToken(hostID, plaintextToken string) error {
	if f.err != nil {
		return f.err
	}
	hash, err := transport.HashToken(plaintextToken)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, struct{ hostID, hash string }{hostID, hash})
	return nil
}

func (f *fakeRegistrar) verify(hostID, token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.hostID != hostID {
			continue
		}
		ok, err := transport.VerifyToken(token, e.hash)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// newEnrollServer returns a Server whose device store already has one
// paired device, plus that device's token.
func newEnrollServer(t *testing.T, reg HostRegistrar) (*Server, string) {
	t.Helper()
	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	return &Server{Devices: devices, Hosts: reg}, deviceToken
}

func postHosts(t *testing.T, srv *Server, deviceToken, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/hosts", reader)
	if deviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+deviceToken)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeCreateHost(t *testing.T, rec *httptest.ResponseRecorder) CreateHostResponse {
	t.Helper()
	var got CreateHostResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return got
}

func TestCreateHost_GeneratesHostIDAndToken(t *testing.T) {
	reg := &fakeRegistrar{}
	srv, deviceToken := newEnrollServer(t, reg)

	rec := postHosts(t, srv, deviceToken, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	got := decodeCreateHost(t, rec)
	if got.HostID == "" {
		t.Fatal("response has no host_id")
	}
	if got.Token == "" {
		t.Fatal("response has no plaintext token")
	}
	if got.HostIDPath == "" || got.TokenPath == "" {
		t.Fatalf("response is missing the agent file paths: %+v", got)
	}
	if !reg.verify(got.HostID, got.Token) {
		t.Fatal("the returned token does not verify against the hash registered for its host_id")
	}
}

func TestCreateHost_PersistsOptionalName(t *testing.T) {
	records := &fakeHostRecords{}
	srv, deviceToken := newEnrollServer(t, &fakeRegistrar{})
	srv.HostRecords = records

	rec := postHosts(t, srv, deviceToken, `{"name":"Production"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if len(records.created) != 1 || records.created[0].name != "Production" || records.created[0].id == "" {
		t.Fatalf("unexpected stored hosts: %+v", records.created)
	}
}

func TestListHosts_RequiresDeviceTokenAndReturnsHostMetadata(t *testing.T) {
	records := &fakeHostRecords{hosts: []schema.Host{{ID: "host-a", Name: "Production", Hostname: "web-01", AgentVersion: "1.2.3", LastSeenAt: time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)}}}
	srv, token := newEnrollServer(t, &fakeRegistrar{})
	srv.HostRecords = records

	unauthorized := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/hosts", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/hosts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []schema.Host
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding hosts: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Production" || got[0].Hostname != "web-01" || got[0].LastSeenAt.IsZero() {
		t.Fatalf("unexpected hosts: %+v", got)
	}
}

func TestCreateHost_EachCallIsDistinct(t *testing.T) {
	reg := &fakeRegistrar{}
	srv, deviceToken := newEnrollServer(t, reg)

	first := decodeCreateHost(t, postHosts(t, srv, deviceToken, ""))
	second := decodeCreateHost(t, postHosts(t, srv, deviceToken, ""))

	if first.HostID == second.HostID {
		t.Fatalf("two enrollments reused host_id %q", first.HostID)
	}
	if first.Token == second.Token {
		t.Fatal("two enrollments reused the same ingest token")
	}
}

// The plaintext token is returned once and never persisted: only its
// Argon2id hash reaches the store, so nothing can hand it back later.
func TestCreateHost_StoresOnlyTheHash(t *testing.T) {
	reg := &fakeRegistrar{}
	srv, deviceToken := newEnrollServer(t, reg)

	got := decodeCreateHost(t, postHosts(t, srv, deviceToken, ""))

	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, e := range reg.entries {
		if strings.Contains(e.hash, got.Token) {
			t.Fatal("the plaintext token leaked into what was persisted")
		}
		if !strings.HasPrefix(e.hash, "$argon2id$") {
			t.Fatalf("persisted value %q is not an Argon2id hash", e.hash)
		}
	}
}

func TestCreateHost_AcceptsCallerSuppliedHostID(t *testing.T) {
	reg := &fakeRegistrar{}
	srv, deviceToken := newEnrollServer(t, reg)

	rec := postHosts(t, srv, deviceToken, `{"host_id":"01J8XQZK9V2M0000000000000A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeCreateHost(t, rec)
	if got.HostID != "01J8XQZK9V2M0000000000000A" {
		t.Fatalf("host_id = %q, want the one the caller sent", got.HostID)
	}
	if !reg.verify(got.HostID, got.Token) {
		t.Fatal("token was not registered against the caller-supplied host_id")
	}
}

func TestCreateHost_RejectsUnauthenticatedCallers(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"no token at all", ""},
		{"a token no device ever held", "not-a-real-device-token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRegistrar{}
			srv, _ := newEnrollServer(t, reg)

			rec := postHosts(t, srv, tc.token, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if len(reg.entries) != 0 {
				t.Fatal("an unauthenticated request still registered a token")
			}
		})
	}
}

// Unlike POST /v1/devices/pair, an empty device store is NOT a bootstrap
// exception here: enrolling a host grants permanent write capability, so
// there is no unauthenticated path to it at all.
func TestCreateHost_NoBootstrapExceptionWithoutPairedDevices(t *testing.T) {
	reg := &fakeRegistrar{}
	srv := &Server{Devices: NewDeviceTokenStore(), Hosts: reg}

	rec := postHosts(t, srv, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if len(reg.entries) != 0 {
		t.Fatal("a host was enrolled with no device paired and no token presented")
	}
}

// A Server built without a device store must not silently serve host
// enrollment unauthenticated the way requireDeviceToken lets read routes
// through.
func TestCreateHost_WithoutDeviceStoreIsUnavailable(t *testing.T) {
	reg := &fakeRegistrar{}
	srv := &Server{Hosts: reg}

	rec := postHosts(t, srv, "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if len(reg.entries) != 0 {
		t.Fatal("a host was enrolled with no device-token auth configured")
	}
}

func TestCreateHost_WithoutRegistrarIsUnavailable(t *testing.T) {
	srv, deviceToken := newEnrollServer(t, nil)
	srv.Hosts = nil

	rec := postHosts(t, srv, deviceToken, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestCreateHost_RejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{"host_id":`},
		{"host_id with a path separator", `{"host_id":"../../etc/passwd"}`},
		{"host_id with shell metacharacters", `{"host_id":"host; rm -rf /"}`},
		{"host_id with whitespace", `{"host_id":"my host"}`},
		{"host_id too long", `{"host_id":"` + strings.Repeat("a", hostIDMaxLen+1) + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRegistrar{}
			srv, deviceToken := newEnrollServer(t, reg)

			rec := postHosts(t, srv, deviceToken, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(reg.entries) != 0 {
				t.Fatal("a rejected request still registered a token")
			}
		})
	}
}

func TestCreateHost_RejectsNonPost(t *testing.T) {
	srv, deviceToken := newEnrollServer(t, &fakeRegistrar{})

	req := httptest.NewRequest(http.MethodPut, "/v1/hosts", nil)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestCreateHost_RegistrarFailureIsNotReportedAsSuccess(t *testing.T) {
	reg := &fakeRegistrar{err: context.DeadlineExceeded}
	srv, deviceToken := newEnrollServer(t, reg)

	rec := postHosts(t, srv, deviceToken, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "token") && strings.Contains(rec.Body.String(), "host_id") {
		t.Fatalf("a failed enrollment still returned credentials: %q", rec.Body.String())
	}
}
