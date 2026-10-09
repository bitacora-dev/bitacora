package hubauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/hubauth/oidctest"
)

const testClientID = "bitacora-test"

type testHub struct {
	server   *httptest.Server
	auth     *hubauth.Authenticator
	provider *oidctest.Provider
	// browser follows redirects and keeps cookies, like a real one.
	browser *http.Client
	// direct stops at the first response, for asserting on redirects.
	direct *http.Client
}

func newTestHub(t *testing.T) *testHub {
	t.Helper()

	provider, err := oidctest.New(testClientID)
	if err != nil {
		t.Fatalf("starting fake provider: %v", err)
	}
	t.Cleanup(provider.Close)

	// The redirect URL must be known before the Authenticator exists, and the
	// server must exist before that URL is known, so the handler is swapped in
	// once both are built.
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	auth, err := hubauth.New(context.Background(), hubauth.Config{
		Issuer:       provider.Issuer(),
		ClientID:     testClientID,
		ClientSecret: "test-secret",
		RedirectURL:  server.URL + "/auth/callback",
		// httptest speaks plain HTTP, and a Secure cookie would never be
		// stored by the jar.
		InsecureCookies: true,
	})
	if err != nil {
		t.Fatalf("building authenticator: %v", err)
	}
	if auth == nil {
		t.Fatal("a fully configured Config must produce an authenticator")
	}

	mux := http.NewServeMux()
	mux.Handle("/auth/", auth.Handler())
	mux.Handle("/", auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("protected"))
	})))
	handler = mux

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building cookie jar: %v", err)
	}
	browser := &http.Client{Jar: jar}
	direct := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &testHub{server: server, auth: auth, provider: provider, browser: browser, direct: direct}
}

func (h *testHub) signIn(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/auth/login", nil)
	if err != nil {
		t.Fatalf("building login request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := h.browser.Do(req)
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in ended with status %d, want 200", resp.StatusCode)
	}
}

func TestSignInGrantsAccessToTheGuardedUI(t *testing.T) {
	hub := newTestHub(t)
	hub.signIn(t)

	resp, err := hub.browser.Get(hub.server.URL + "/")
	if err != nil {
		t.Fatalf("requesting the guarded page: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("guarded page returned %d after sign-in, want 200", resp.StatusCode)
	}
}

// The PKCE challenge is not decoration: without it an intercepted code can be
// redeemed by someone else.
func TestSignInSendsAPKCEChallenge(t *testing.T) {
	hub := newTestHub(t)
	hub.signIn(t)

	if hub.provider.LastChallenge == "" {
		t.Fatal("the client authorized without sending a PKCE challenge")
	}
}

func TestMeReportsTheSignedInIdentity(t *testing.T) {
	hub := newTestHub(t)
	hub.signIn(t)

	resp, err := hub.browser.Get(hub.server.URL + "/auth/me")
	if err != nil {
		t.Fatalf("requesting the identity: %v", err)
	}
	defer resp.Body.Close()

	var identity hubauth.Identity
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		t.Fatalf("decoding the identity: %v", err)
	}
	if identity.Subject != hub.provider.Subject {
		t.Fatalf("subject is %q, want %q", identity.Subject, hub.provider.Subject)
	}
	if identity.Email != hub.provider.Email {
		t.Fatalf("email is %q, want %q", identity.Email, hub.provider.Email)
	}
}

// A callback carrying a state this browser never started must not create a
// session: that is the cross-site request forgery this check exists for.
func TestCallbackRejectsAStateThisBrowserNeverStarted(t *testing.T) {
	hub := newTestHub(t)

	resp, err := hub.direct.Get(hub.server.URL + "/auth/callback?state=someone-elses&code=whatever")
	if err != nil {
		t.Fatalf("calling back: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign state returned %d, want 400", resp.StatusCode)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	hub := newTestHub(t)
	hub.signIn(t)

	resp, err := hub.browser.Get(hub.server.URL + "/auth/logout")
	if err != nil {
		t.Fatalf("logging out: %v", err)
	}
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodGet, hub.server.URL+"/", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	after, err := hub.direct.Do(req)
	if err != nil {
		t.Fatalf("requesting the guarded page: %v", err)
	}
	defer after.Body.Close()
	if after.StatusCode != http.StatusFound {
		t.Fatalf("guarded page returned %d after logout, want a redirect to the provider", after.StatusCode)
	}
}

// A browser gets redirected, but anything asking for data gets a 401 it can
// act on instead of an HTML page it cannot parse.
func TestDataRequestsWithoutASessionGet401(t *testing.T) {
	hub := newTestHub(t)

	resp, err := hub.direct.Get(hub.server.URL + "/")
	if err != nil {
		t.Fatalf("requesting without a session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("data request returned %d, want 401", resp.StatusCode)
	}
}

// Leaving the variables unset is the supported way to keep the hub behaving
// exactly as it did before ADR-0019.
func TestAnUnconfiguredHubHasNoAuthenticator(t *testing.T) {
	auth, err := hubauth.New(context.Background(), hubauth.Config{})
	if err != nil {
		t.Fatalf("an empty config must not be an error: %v", err)
	}
	if auth != nil {
		t.Fatal("an empty config produced an authenticator")
	}
	if (hubauth.Config{Issuer: "https://idp.example", ClientID: "id"}).Enabled() {
		t.Fatal("a config without a redirect URL reported itself enabled")
	}
}

// return_to is attacker-controllable, so it must never send a browser off this
// hub after a successful login.
func TestLoginRefusesToRedirectOffTheHub(t *testing.T) {
	hub := newTestHub(t)

	req, err := http.NewRequest(http.MethodGet, hub.server.URL+"/auth/login?return_to=https://evil.example/steal", nil)
	if err != nil {
		t.Fatalf("building login request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := hub.browser.Do(req)
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Request.URL.String(); !strings.HasPrefix(got, hub.server.URL) {
		t.Fatalf("login ended at %q, outside the hub", got)
	}
}
