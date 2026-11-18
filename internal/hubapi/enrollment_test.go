package hubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/transport"
)

// fakeTokenAdmin is the lifecycle side of the token store, backed by the
// real Argon2id helpers so a test can assert that a returned plaintext token
// verifies against what was persisted.
type fakeTokenAdmin struct {
	mu       sync.Mutex
	tokens   []transport.TokenInfo
	hashes   map[int64]string
	nextID   int64
	addErr   error
	listErr  error
	rotatErr error
}

func newFakeTokenAdmin() *fakeTokenAdmin {
	return &fakeTokenAdmin{hashes: make(map[int64]string)}
}

func (f *fakeTokenAdmin) AddLabeledToken(_ context.Context, hostID, label, plaintext string) (transport.TokenInfo, error) {
	if f.addErr != nil {
		return transport.TokenInfo{}, f.addErr
	}
	hash, err := transport.HashToken(plaintext)
	if err != nil {
		return transport.TokenInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	info := transport.TokenInfo{ID: f.nextID, HostID: hostID, Label: label, CreatedAt: time.Now().UTC()}
	f.tokens = append(f.tokens, info)
	f.hashes[info.ID] = hash
	return info, nil
}

func (f *fakeTokenAdmin) AddToken(hostID, plaintext string) error {
	_, err := f.AddLabeledToken(context.Background(), hostID, "", plaintext)
	return err
}

func (f *fakeTokenAdmin) ListTokens(context.Context) ([]transport.TokenInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]transport.TokenInfo, len(f.tokens))
	copy(out, f.tokens)
	return out, nil
}

func (f *fakeTokenAdmin) RevokeHostTokens(_ context.Context, hostID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	revoked := 0
	for i := range f.tokens {
		if f.tokens[i].HostID == hostID && !f.tokens[i].Revoked() {
			f.tokens[i].RevokedAt = time.Now().UTC()
			revoked++
		}
	}
	return revoked, nil
}

func (f *fakeTokenAdmin) RotateHostTokens(ctx context.Context, hostID, label, plaintext string, grace time.Duration) (transport.TokenInfo, error) {
	if f.rotatErr != nil {
		return transport.TokenInfo{}, f.rotatErr
	}
	f.mu.Lock()
	previous := int64(0)
	for i := range f.tokens {
		if f.tokens[i].HostID == hostID {
			previous = f.tokens[i].ID
			if grace > 0 {
				f.tokens[i].ExpiresAt = time.Now().UTC().Add(grace)
			} else {
				f.tokens[i].RevokedAt = time.Now().UTC()
			}
		}
	}
	f.mu.Unlock()
	if previous == 0 {
		// Mirrors the real store: rotation replaces a credential, it never
		// mints a first one for a host nobody enrolled.
		return transport.TokenInfo{}, fmt.Errorf("rotating host %s: %w", hostID, transport.ErrNoCredential)
	}

	info, err := f.AddLabeledToken(ctx, hostID, label, plaintext)
	if err != nil {
		return transport.TokenInfo{}, err
	}
	info.RotatedFrom = previous
	return info, nil
}

func (f *fakeTokenAdmin) verify(hostID, plaintext string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, token := range f.tokens {
		if token.HostID != hostID {
			continue
		}
		ok, err := transport.VerifyToken(plaintext, f.hashes[token.ID])
		if err == nil && ok {
			return true
		}
	}
	return false
}

// fakeEnrollments is an in-memory EnrollmentIssuer. It stores plaintext
// codes, which the real store never does — a fake is allowed to, and it
// keeps the test's assertions about the *route* readable.
type fakeEnrollments struct {
	mu        sync.Mutex
	codes     map[string]transport.EnrollmentCode
	createErr error
	redeemErr error
	redeems   int
}

func newFakeEnrollments() *fakeEnrollments {
	return &fakeEnrollments{codes: make(map[string]transport.EnrollmentCode)}
}

func (f *fakeEnrollments) CreateEnrollmentCode(_ context.Context, plaintext string, code transport.EnrollmentCode) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[plaintext] = code
	return nil
}

func (f *fakeEnrollments) RedeemEnrollmentCode(_ context.Context, plaintext string) (transport.EnrollmentCode, bool, error) {
	if f.redeemErr != nil {
		return transport.EnrollmentCode{}, false, f.redeemErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redeems++
	code, ok := f.codes[plaintext]
	if !ok {
		return transport.EnrollmentCode{}, false, nil
	}
	delete(f.codes, plaintext)
	return code, true, nil
}

// newEnrollmentServer wires a hub with a paired device, the token lifecycle
// and the enrollment store, which is the production shape.
func newEnrollmentServer(t *testing.T) (*Server, *fakeTokenAdmin, *fakeEnrollments, string) {
	t.Helper()
	devices := NewDeviceTokenStore()
	_, deviceToken, _, err := devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}
	admin := newFakeTokenAdmin()
	enrollments := newFakeEnrollments()
	srv := &Server{
		Devices:     devices,
		Hosts:       admin,
		Tokens:      admin,
		Enrollments: enrollments,
		HostRecords: &fakeHostRecords{},
	}
	return srv, admin, enrollments, deviceToken
}

func post(t *testing.T, srv *Server, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCreateEnrollmentReturnsAOneTimeCodeAndTheCommandToUseIt(t *testing.T) {
	srv, _, enrollments, deviceToken := newEnrollmentServer(t)

	rec := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps-berlin"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got CreateEnrollmentResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Code == "" {
		t.Fatal("response carries no enrollment code")
	}
	if got.Label != "vps-berlin" {
		t.Fatalf("label = %q, want vps-berlin", got.Label)
	}
	if got.ExpiresAt.IsZero() || got.ExpiresAt.After(time.Now().UTC().Add(DefaultEnrollmentCodeTTL+time.Minute)) {
		t.Fatalf("expires_at = %s, want roughly %s from now", got.ExpiresAt, DefaultEnrollmentCodeTTL)
	}
	if !strings.Contains(got.EnrollCommand, "bitacora-agent enroll") || !strings.Contains(got.EnrollCommand, "-code -") {
		t.Fatalf("enroll_command = %q, want the stdin form of the agent command", got.EnrollCommand)
	}
	if stored, ok := enrollments.codes[got.Code]; !ok || stored.Label != "vps-berlin" {
		t.Fatalf("stored codes = %+v, want the issued one bound to its label", enrollments.codes)
	}
}

func TestCreateEnrollmentRejectsAnUnauthenticatedCaller(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)

	rec := post(t, srv, "/v1/enrollments", "", `{"name":"vps"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: an unauthenticated caller must not be able to mint an enrollment code", rec.Code, http.StatusUnauthorized)
	}
}

// ADR-0023: view does not authorize an operation, and minting a credential
// for a new server is an operation.
func TestCreateEnrollmentRejectsAHumanWithoutOperate(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)
	srv.Humans = fakeHumanIdentity{identity: localOperator}
	srv.HostScopes = assignedScopes{localOperator.Subject: {AllHosts: hubauth.HostCapabilities{View: true}}}

	rec := post(t, srv, "/v1/enrollments", "", `{"name":"vps"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCreateEnrollmentAcceptsAHumanWithOperate(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)
	srv.Humans = fakeHumanIdentity{identity: localOperator}
	srv.HostScopes = assignedScopes{localOperator.Subject: {AllHosts: hubauth.HostCapabilities{View: true, Operate: true}}}

	rec := post(t, srv, "/v1/enrollments", "", `{"name":"vps"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestEnrollExchangesACodeForAHostIDAndToken(t *testing.T) {
	srv, admin, enrollments, deviceToken := newEnrollmentServer(t)

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps-berlin"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding issued code: %v", err)
	}

	rec := post(t, srv, "/v1/enroll", "", `{"code":"`+issued.Code+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got EnrollResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.HostID == "" || got.Token == "" {
		t.Fatalf("response = %+v, want a host_id and a token", got)
	}
	if got.TokenPath == "" || got.HostIDPath == "" {
		t.Fatalf("response = %+v, want the agent file paths", got)
	}
	if !admin.verify(got.HostID, got.Token) {
		t.Fatal("the returned token does not verify against the hash registered for its host_id")
	}
	if len(enrollments.codes) != 0 {
		t.Fatalf("the code survived its redemption: %+v", enrollments.codes)
	}
}

// The label the operator typed when minting the code is what names the
// agent, so it has to reach both the credential and the host record.
func TestEnrollCarriesTheLabelOntoTheCredentialAndTheHostRecord(t *testing.T) {
	srv, admin, _, deviceToken := newEnrollmentServer(t)
	records := &fakeHostRecords{}
	srv.HostRecords = records

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps-berlin"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding issued code: %v", err)
	}
	if rec := post(t, srv, "/v1/enroll", "", `{"code":"`+issued.Code+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %q)", rec.Code, rec.Body.String())
	}

	tokens, err := admin.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Label != "vps-berlin" {
		t.Fatalf("tokens = %+v, want one labelled vps-berlin", tokens)
	}
	if len(records.created) != 1 || records.created[0].name != "vps-berlin" {
		t.Fatalf("host records = %+v, want one named vps-berlin", records.created)
	}
}

// A code pinned to a host_id is how a machine that already has an identity
// is re-enrolled without being handed a second one.
func TestEnrollHonoursThePinnedHostID(t *testing.T) {
	srv, _, _, deviceToken := newEnrollmentServer(t)

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps","host_id":"01JPINNED"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	rec := post(t, srv, "/v1/enroll", "", `{"code":"`+issued.Code+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %q)", rec.Code, rec.Body.String())
	}
	var got EnrollResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.HostID != "01JPINNED" {
		t.Fatalf("host_id = %q, want the pinned 01JPINNED", got.HostID)
	}
}

// A machine presenting a host_id the code was not issued for must not be
// able to redirect the enrollment, and must not learn that it failed for
// that reason.
func TestEnrollRefusesAHostIDThatContradictsTheCode(t *testing.T) {
	srv, _, _, deviceToken := newEnrollmentServer(t)

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps","host_id":"01JPINNED"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	rec := post(t, srv, "/v1/enroll", "", `{"code":"`+issued.Code+`","host_id":"01JSOMEONEELSE"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(rec.Body.String(), enrollRejection) {
		t.Fatalf("body = %q, want the generic rejection", rec.Body.String())
	}
}

// An unpinned code lets the agent keep the host_id it already generated for
// itself, which is what makes re-enrolling an existing agent lossless.
func TestEnrollAcceptsTheAgentsExistingHostIDWhenTheCodeIsUnpinned(t *testing.T) {
	srv, _, _, deviceToken := newEnrollmentServer(t)

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	rec := post(t, srv, "/v1/enroll", "", `{"code":"`+issued.Code+`","host_id":"01JEXISTING"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %q)", rec.Code, rec.Body.String())
	}
	var got EnrollResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.HostID != "01JEXISTING" {
		t.Fatalf("host_id = %q, want the agent's own 01JEXISTING", got.HostID)
	}
}

// Every failure mode answers the same thing, so probing cannot tell an
// unknown code from an expired or already-redeemed one.
func TestEnrollAnswersEveryFailureIdentically(t *testing.T) {
	for _, body := range []string{`{}`, `{"code":""}`, `{"code":"never-issued"}`} {
		srv, _, _, _ := newEnrollmentServer(t)
		rec := post(t, srv, "/v1/enroll", "", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("body %s: status = %d, want %d", body, rec.Code, http.StatusUnauthorized)
		}
		if !strings.Contains(rec.Body.String(), enrollRejection) {
			t.Fatalf("body %s: response = %q, want the generic rejection", body, rec.Body.String())
		}
	}
}

// The code is the only credential on this route, so an online guess has to
// run out of budget long before it runs out of codes.
func TestEnrollRateLimitsPerSourceAddress(t *testing.T) {
	srv, _, _, _ := newEnrollmentServer(t)

	limited := false
	for attempt := range enrollBurst + 3 {
		req := httptest.NewRequest(http.MethodPost, "/v1/enroll", strings.NewReader(`{"code":"guess"}`))
		req.RemoteAddr = "203.0.113.7:4242"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			if attempt < enrollBurst {
				t.Fatalf("attempt %d was limited inside the burst of %d", attempt, enrollBurst)
			}
			break
		}
	}
	if !limited {
		t.Fatalf("no attempt beyond the burst of %d was rate limited", enrollBurst)
	}
}

// The budget is per source address: one noisy host must not lock out the
// machine actually being enrolled. The ephemeral port is not part of the
// key, or every new connection would get a fresh budget.
func TestEnrollRateLimitIsPerAddressAndIgnoresThePort(t *testing.T) {
	srv, _, _, deviceToken := newEnrollmentServer(t)

	for range enrollBurst + 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/enroll", strings.NewReader(`{"code":"guess"}`))
		req.RemoteAddr = "203.0.113.7:4242"
		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}
	// Same address, different port: still out of budget.
	req := httptest.NewRequest(http.MethodPost, "/v1/enroll", strings.NewReader(`{"code":"guess"}`))
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d: a new source port must not reset the budget", rec.Code, http.StatusTooManyRequests)
	}

	created := post(t, srv, "/v1/enrollments", deviceToken, `{"name":"vps"}`)
	var issued CreateEnrollmentResponse
	if err := json.NewDecoder(created.Body).Decode(&issued); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	other := httptest.NewRequest(http.MethodPost, "/v1/enroll", strings.NewReader(`{"code":"`+issued.Code+`"}`))
	other.RemoteAddr = "198.51.100.9:4242"
	otherRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(otherRec, other)
	if otherRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: a different address has its own budget (body %q)", otherRec.Code, http.StatusCreated, otherRec.Body.String())
	}
}

func TestEnrollmentRoutesAnswer503WhenNotConfigured(t *testing.T) {
	srv := &Server{Devices: NewDeviceTokenStore()}
	for _, path := range []string{"/v1/enrollments", "/v1/enroll"} {
		rec := post(t, srv, path, "", `{}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusServiceUnavailable)
		}
	}
}
