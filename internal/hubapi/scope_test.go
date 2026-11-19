package hubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// The two hosts every case below uses: one the identity is related to, one it
// is not. Nothing about hostB may reach a response, not even the fact that it
// exists.
const (
	hostA = "host-a"
	hostB = "host-b"
)

// assignedScopes stands in for the per-identity relation ADR-0023 defines but
// deliberately does not yet persist: there is no assignment API or UI in this
// change, so a test is the only place a partial scope exists.
type assignedScopes map[string]hubauth.HostScope

func (a assignedScopes) HostScope(identity hubauth.Identity) hubauth.HostScope {
	return a[identity.Subject]
}

// scopedJobPoller serves one job per host so a scoped read of hostA's job can
// be told apart from a scoped read of hostB's.
type scopedJobPoller struct{}

func (scopedJobPoller) GetJob(_ context.Context, hostID, jobID string) (schema.Job, bool, error) {
	if jobID != jobIDFor(hostID) {
		return schema.Job{}, false, nil
	}
	return schema.Job{ID: jobID, JobName: "backup", HostID: hostID, Status: schema.JobSuccess, Schema: schema.CurrentSchemaVersion}, true, nil
}

func (scopedJobPoller) ListJobOutput(context.Context, string, string, int64, int) ([]schema.JobOutputLine, int64, error) {
	return nil, 0, nil
}

func jobIDFor(hostID string) string { return "job-" + hostID }

// localOperator is the identity the installation creates, and the one whose
// behaviour must not change: view and operate on every host.
var localOperator = hubauth.Identity{Source: "local", Subject: "local:operator", Name: "Local operator"}

func scopedTestServer(humans HumanIdentityProvider, scopes HostScopeResolver) *Server {
	now := time.Now().UTC()
	srv := &Server{
		Metrics: &fakeMetrics{},
		Events: &fakeEvents{events: []schema.Event{
			{ID: "event-" + hostA, TS: now, HostID: hostA, Source: "test", Type: "test.event", Severity: schema.SeverityInfo, Title: "on " + hostA, Schema: schema.CurrentSchemaVersion},
			{ID: "event-" + hostB, TS: now, HostID: hostB, Source: "test", Type: "test.event", Severity: schema.SeverityInfo, Title: "on " + hostB, Schema: schema.CurrentSchemaVersion},
		}},
		Jobs:      &fakeJobs{},
		JobPoller: scopedJobPoller{},
		Logs: &fakeLogs{entries: []logstore.Entry{
			{ID: "block:1", TS: now, HostID: hostA, Source: "journald", Message: "line on " + hostA},
			{ID: "block:2", TS: now, HostID: hostB, Source: "journald", Message: "line on " + hostB},
		}},
		Inventories: &fakeInventories{byHostKind: map[string]schema.Inventory{
			hostA + "/" + string(schema.InventoryShare): sampleShareInventory(hostA),
			hostB + "/" + string(schema.InventoryShare): sampleShareInventory(hostB),
		}},
		HostRecords: &fakeHostRecords{hosts: []schema.Host{{ID: hostA, Name: "A"}, {ID: hostB, Name: "B"}}},
		Humans:      humans,
		Actions:     actionconfirm.NewStore(),
		HostScopes:  scopes,
	}
	return srv
}

// perHostRoute is one read or action route that names a host in its request.
// Every such route in internal/hubapi is listed here on purpose: the leak this
// guards against is a route nobody remembered to filter.
type perHostRoute struct {
	name string
	// request builds a request for one host.
	request func(hostID string) *http.Request
	// allowedStatus is what the route answers for a host in scope.
	//
	// The action confirmation route answers 401 there: it is reached with no
	// issued token, which is exactly what proves the scope guard let it
	// through instead of short-circuiting to 404.
	allowedStatus int
}

func perHostRoutes() []perHostRoute {
	window := "from=" + rfc3339(time.Now().UTC().Add(-time.Hour)) + "&to=" + rfc3339(time.Now().UTC().Add(time.Hour))
	return []perHostRoute{
		{
			name:          "GET /v1/summary",
			request:       func(hostID string) *http.Request { return get("/v1/summary?host_id=" + hostID) },
			allowedStatus: http.StatusOK,
		},
		{
			name:          "GET /v1/events",
			request:       func(hostID string) *http.Request { return get("/v1/events?host_id=" + hostID + "&" + window) },
			allowedStatus: http.StatusOK,
		},
		{
			name:          "GET /v1/logs",
			request:       func(hostID string) *http.Request { return get("/v1/logs?host_id=" + hostID + "&" + window) },
			allowedStatus: http.StatusOK,
		},
		{
			name: "GET /v1/inventory",
			request: func(hostID string) *http.Request {
				return get("/v1/inventory?host_id=" + hostID + "&kind=" + string(schema.InventoryShare))
			},
			allowedStatus: http.StatusOK,
		},
		{
			name: "GET /v1/jobs/{id}",
			request: func(hostID string) *http.Request {
				return get("/v1/jobs/" + jobIDFor(hostID) + "?host_id=" + hostID)
			},
			allowedStatus: http.StatusOK,
		},
		{
			name: "POST /v1/actions/package-operations/token",
			request: func(hostID string) *http.Request {
				return actionPost("/v1/actions/package-operations/token", hostID, "")
			},
			allowedStatus: http.StatusCreated,
		},
		{
			name: "POST /v1/actions/package-operations/confirm",
			request: func(hostID string) *http.Request {
				return actionPost("/v1/actions/package-operations/confirm", hostID, `,"request_id":"req","action_token":"nope"`)
			},
			allowedStatus: http.StatusUnauthorized,
		},
	}
}

func get(target string) *http.Request { return httptest.NewRequest(http.MethodGet, target, nil) }

func actionPost(target, hostID, extra string) *http.Request {
	body := fmt.Sprintf(`{"host_id":%q,"operation":"REFRESH_PACKAGE_CACHE"%s}`, hostID, extra)
	return httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
}

func rfc3339(ts time.Time) string { return ts.Format(time.RFC3339) }

func serve(srv *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

// TestPerHostRoutesAnswer404ForAHostOutsideTheIdentityScope is the core
// ADR-0023 guarantee: a host the signed-in person is not related to must be
// indistinguishable from a host that does not exist.
func TestPerHostRoutesAnswer404ForAHostOutsideTheIdentityScope(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "oidc", Subject: "viewer-a"}}
	scopes := assignedScopes{"viewer-a": {Hosts: map[string]hubauth.HostCapabilities{hostA: {View: true, Operate: true}}}}

	for _, route := range perHostRoutes() {
		t.Run(route.name, func(t *testing.T) {
			srv := scopedTestServer(humans, scopes)

			allowed := serve(srv, route.request(hostA))
			if allowed.Code != route.allowedStatus {
				t.Fatalf("%s for the host in scope = %d, want %d: %s", route.name, allowed.Code, route.allowedStatus, allowed.Body.String())
			}

			denied := serve(scopedTestServer(humans, scopes), route.request(hostB))
			if denied.Code != http.StatusNotFound {
				t.Fatalf("%s for the host out of scope = %d, want 404: %s", route.name, denied.Code, denied.Body.String())
			}
			if denied.Code == http.StatusForbidden {
				t.Fatalf("%s answered 403, which confirms the host exists", route.name)
			}
			if body := denied.Body.String(); strings.Contains(body, hostB) {
				t.Fatalf("%s leaked the out-of-scope host id in its body: %s", route.name, body)
			}
		})
	}
}

// TestPerHostRoutesStayOpenForTheInitialLocalOperator pins the behaviour the
// single operator already has. Enforcing scope must not be the change that
// takes their own dashboard away.
func TestPerHostRoutesStayOpenForTheInitialLocalOperator(t *testing.T) {
	humans := fakeHumanIdentity{identity: localOperator}
	for _, route := range perHostRoutes() {
		t.Run(route.name, func(t *testing.T) {
			for _, hostID := range []string{hostA, hostB} {
				// A fresh server per request: the action routes are
				// single-use by design and must not be reached twice.
				rec := serve(scopedTestServer(humans, nil), route.request(hostID))
				if rec.Code != route.allowedStatus {
					t.Fatalf("%s for %s as local:operator = %d, want %d: %s", route.name, hostID, rec.Code, route.allowedStatus, rec.Body.String())
				}
			}
		})
	}
}

func TestListHostsReturnsOnlyTheHostsTheIdentityCanView(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "oidc", Subject: "viewer-a"}}
	scopes := assignedScopes{"viewer-a": {Hosts: map[string]hubauth.HostCapabilities{hostA: {View: true}}}}

	rec := serve(scopedTestServer(humans, scopes), get("/v1/hosts"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/hosts = %d: %s", rec.Code, rec.Body.String())
	}
	var hosts []schema.Host
	if err := json.NewDecoder(rec.Body).Decode(&hosts); err != nil {
		t.Fatalf("decoding host list: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != hostA {
		t.Fatalf("host list = %+v, want only %s", hosts, hostA)
	}
}

func TestListHostsIsEmptyRatherThanNullForAnIdentityWithNoHosts(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "oidc", Subject: "viewer-none"}}
	rec := serve(scopedTestServer(humans, assignedScopes{}), get("/v1/hosts"))
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("host list for an identity with no hosts = %q, want []", body)
	}
}

func TestListHostsReturnsEveryHostForTheInitialLocalOperator(t *testing.T) {
	humans := fakeHumanIdentity{identity: localOperator}
	rec := serve(scopedTestServer(humans, nil), get("/v1/hosts"))
	var hosts []schema.Host
	if err := json.NewDecoder(rec.Body).Decode(&hosts); err != nil {
		t.Fatalf("decoding host list: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("host list for local:operator = %+v, want both hosts", hosts)
	}
}

// TestViewDoesNotAuthorizeAnOperation is ADR-0023's explicit rule: view never
// implies operate, and a host that cannot be operated answers 404 on the
// action routes even though the very same identity can read it.
func TestViewDoesNotAuthorizeAnOperation(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "oidc", Subject: "viewer-a"}}
	scopes := assignedScopes{"viewer-a": {Hosts: map[string]hubauth.HostCapabilities{hostA: {View: true}}}}
	srv := scopedTestServer(humans, scopes)

	if rec := serve(srv, get("/v1/summary?host_id="+hostA)); rec.Code != http.StatusOK {
		t.Fatalf("reading a viewable host = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec := serve(srv, actionPost("/v1/actions/package-operations/token", hostA, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("operating a view-only host = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestDeviceTokensGainNoHumanScope keeps the two boundaries apart. A paired
// device is not a person: it keeps the ADR-0014 reach it already had, and it
// never acquires the human capability to operate a host.
func TestDeviceTokensGainNoHumanScope(t *testing.T) {
	devices := NewDeviceTokenStore()
	_, token, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting a device token: %v", err)
	}
	// An identity that would see nothing if a device token could borrow it.
	humans := fakeHumanIdentity{}
	srv := scopedTestServer(humans, assignedScopes{})
	srv.Devices = devices

	withToken := func(r *http.Request) *http.Request {
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}

	rec := serve(srv, withToken(get("/v1/hosts")))
	var hosts []schema.Host
	if err := json.NewDecoder(rec.Body).Decode(&hosts); err != nil {
		t.Fatalf("decoding host list: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("device-token host list = %+v, want the ADR-0014 reach of both hosts", hosts)
	}
	if rec := serve(srv, withToken(get("/v1/summary?host_id="+hostB))); rec.Code != http.StatusOK {
		t.Fatalf("device-token summary = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// Operating a host is a human capability and a device token has none.
	if rec := serve(srv, withToken(actionPost("/v1/actions/package-operations/token", hostA, ""))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("device-token package operation = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// TestUnknownIdentitySourceSeesNothing fails closed: an identity shape no
// login route produces must not inherit the installation operator's reach.
func TestUnknownIdentitySourceSeesNothing(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "made-up", Subject: "local:operator"}}
	srv := scopedTestServer(humans, nil)

	if rec := serve(srv, get("/v1/summary?host_id="+hostA)); rec.Code != http.StatusNotFound {
		t.Fatalf("summary for an unrecognized identity source = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	rec := serve(srv, get("/v1/hosts"))
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("host list for an unrecognized identity source = %q, want []", body)
	}
}

// TestRestrictedIdentityCannotTellAnUnknownHostFromSomeoneElses closes the
// inverse leak: if an out-of-scope host answered 404 while an unknown host
// answered 200, the status code itself would enumerate real servers.
func TestRestrictedIdentityCannotTellAnUnknownHostFromSomeoneElses(t *testing.T) {
	humans := fakeHumanIdentity{identity: hubauth.Identity{Source: "oidc", Subject: "viewer-a"}}
	scopes := assignedScopes{"viewer-a": {Hosts: map[string]hubauth.HostCapabilities{hostA: {View: true}}}}
	srv := scopedTestServer(humans, scopes)

	existing := serve(srv, get("/v1/summary?host_id="+hostB))
	unknown := serve(srv, get("/v1/summary?host_id=host-never-enrolled"))
	if existing.Code != unknown.Code {
		t.Fatalf("existing out-of-scope host = %d but unknown host = %d; the status code enumerates hosts", existing.Code, unknown.Code)
	}
	if existing.Body.String() != unknown.Body.String() {
		t.Fatalf("out-of-scope body %q differs from unknown-host body %q", existing.Body.String(), unknown.Body.String())
	}
}
