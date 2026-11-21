package main

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

var installTokenLine = regexp.MustCompile(`(?m)^bitacora-hub:     (\S+)$`)

// announceForTest runs the startup announcement and returns the one token it
// printed, failing if it printed anything but exactly one.
func announceForTest(t *testing.T, h *hub, store *hubauth.LocalStore) (string, string) {
	t.Helper()
	var out bytes.Buffer
	if err := announceSetup(h.auth, store, time.Minute, &out); err != nil {
		t.Fatalf("announceSetup: %v", err)
	}
	matches := installTokenLine.FindAllStringSubmatch(out.String(), -1)
	if len(matches) != 1 {
		t.Fatalf("announceSetup printed %d tokens, want exactly 1:\n%s", len(matches), out.String())
	}
	return matches[0][1], out.String()
}

func noRedirectClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func postJSON(t *testing.T, client *http.Client, url string, body any) (*http.Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, payload
}

// The breaking change of ADR-0025: a hub with no login source no longer
// serves its dashboard or data to whoever reaches the origin. Only the setup
// screen, the session probe and the agent paths remain.
func TestHubWithoutALoginSourceFailsClosed(t *testing.T) {
	h, _, err := newSetupModeHub(t.TempDir())
	if err != nil {
		t.Fatalf("newSetupModeHub: %v", err)
	}
	defer h.Close()
	const hostID = "host-setup-mode"
	if err := h.tokens.AddToken(hostID, "machine-token"); err != nil {
		t.Fatalf("adding ingest token: %v", err)
	}
	baseURL := serveHub(t, h)
	client := noRedirectClient(t)

	response, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/auth/setup" {
		t.Fatalf("GET / = %d %q; want a redirect to /auth/setup", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(baseURL + "/auth/setup")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /auth/setup = %d %q; want the embedded shell", response.StatusCode, response.Header.Get("Content-Type"))
	}

	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/v1/summary?host_id=" + hostID},
		{http.MethodGet, "/v1/hosts"},
		{http.MethodGet, "/v1/events"},
		{http.MethodPost, "/v1/hosts"},
		{http.MethodPost, "/v1/devices/pair"},
		{http.MethodPost, "/v1/enrollments"},
	} {
		req, err := http.NewRequest(probe.method, baseURL+probe.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer any-device-token")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d; want 503 setup_required", probe.method, probe.path, response.StatusCode)
		}
	}

	session := authSession(t, client, baseURL)
	if session["setup_required"] != true || session["setup_available"] != false {
		t.Fatalf("/v1/auth/session = %v; want setup required and no token yet", session)
	}

	ingest := &transport.Client{BaseURL: baseURL, Token: "machine-token"}
	if _, err := ingest.Send(t.Context(), &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
		LogLines: []*bitacorapb.LogLine{{
			TsMs: time.Now().UnixMilli(), HostId: hostID, Source: "journald", Message: "setup mode must not silence the agent",
		}},
	}); err != nil {
		t.Fatalf("ingest in setup mode: %v", err)
	}
}

// A restart without a credential issues a new install token and the previous
// one stops working: only the hash is kept, so it cannot be shown again.
func TestInstallTokenIsRegeneratedOnRestart(t *testing.T) {
	dataDir := t.TempDir()
	h, store, err := newSetupModeHub(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	first, out := announceForTest(t, h, store)
	h.Close()
	if !strings.Contains(out, "/auth/setup") || !strings.Contains(out, "auth local recover") {
		t.Errorf("announcement does not explain the next step:\n%s", out)
	}
	raw, err := os.ReadFile(store.SetupTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), first) {
		t.Fatal("the install token is persisted in plaintext")
	}

	h, store, err = newSetupModeHub(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	second, _ := announceForTest(t, h, store)
	if first == second {
		t.Fatal("a restart reused the install token")
	}
	if _, err := store.CheckSetupToken(first); err == nil {
		t.Fatal("the token from the previous start still works")
	}
	if _, err := store.CheckSetupToken(second); err != nil {
		t.Fatalf("the new token does not work: %v", err)
	}
}

// completeWebSetup walks the two setup steps the web UI performs and returns
// the TOTP secret and recovery codes it was shown.
func completeWebSetup(t *testing.T, client *http.Client, baseURL, token, username, password string) (string, []string) {
	t.Helper()
	response, payload := postJSON(t, client, baseURL+"/auth/setup/begin", map[string]string{"token": token, "username": username, "password": password})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("setup begin = %d %s", response.StatusCode, payload)
	}
	var begun struct {
		EnrollmentID string `json:"enrollment_id"`
		TOTPSecret   string `json:"totp_secret"`
	}
	if err := json.Unmarshal(payload, &begun); err != nil {
		t.Fatal(err)
	}
	response, payload = postJSON(t, client, baseURL+"/auth/setup/complete", map[string]string{"token": token, "enrollment_id": begun.EnrollmentID, "totp": totpCode(t, begun.TOTPSecret, time.Now())})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("setup complete = %d %s", response.StatusCode, payload)
	}
	var completed struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(payload, &completed); err != nil {
		t.Fatal(err)
	}
	return begun.TOTPSecret, completed.RecoveryCodes
}

// The whole first run, end to end through the real wiring: install token from
// the console, web setup, dashboard open with the new session, sign in again
// with the username, and the token burned.
func TestFirstRunWebSetupOpensTheHub(t *testing.T) {
	t.Setenv(hubauth.EnvInsecureCookies, "true")
	h, store, err := newSetupModeHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	token, _ := announceForTest(t, h, store)
	baseURL := serveHub(t, h)
	client := noRedirectClient(t)

	if session := authSession(t, client, baseURL); session["setup_available"] != true {
		t.Fatalf("/v1/auth/session = %v; want a redeemable token", session)
	}
	const password = "first run password"
	secret, recovery := completeWebSetup(t, client, baseURL, token, "first-operator", password)
	if len(recovery) != 10 {
		t.Fatalf("got %d recovery codes, want 10", len(recovery))
	}

	session := authSession(t, client, baseURL)
	identity, _ := session["identity"].(map[string]any)
	if session["authenticated"] != true || session["setup_required"] != false || session["setup_available"] != false || identity["subject"] != "local:operator" {
		t.Fatalf("/v1/auth/session after setup = %v; want local:operator signed in and setup gone", session)
	}
	dashboard, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	dashboard.Body.Close()
	if dashboard.StatusCode != http.StatusOK {
		t.Fatalf("dashboard after setup = %d, want 200", dashboard.StatusCode)
	}

	response, _ := postJSON(t, client, baseURL+"/auth/setup/begin", map[string]string{"token": token, "username": "first-operator", "password": password})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reusing the burned token = %d, want 401", response.StatusCode)
	}

	fresh := noRedirectClient(t)
	// Different code window from the one setup consumed is not required: the
	// login simply verifies the TOTP again.
	response, _ = postJSON(t, fresh, baseURL+"/auth/local/login", map[string]string{"username": "someone", "password": password, "totp": totpCode(t, secret, time.Now())})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with the wrong username = %d, want 401", response.StatusCode)
	}
	response, payload := postJSON(t, fresh, baseURL+"/auth/local/login", map[string]string{"username": "first-operator", "password": password, "totp": totpCode(t, secret, time.Now())})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login after setup = %d %s", response.StatusCode, payload)
	}
}

// Total recovery: the local command prints a recovery token, the web setup
// redeems it, the old credential stops working and every old session is
// revoked.
func TestLocalRecoveryCommandReplacesTheCredentialThroughTheWeb(t *testing.T) {
	t.Setenv(hubauth.EnvInsecureCookies, "true")
	h, store, err := newSetupModeHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	install, _ := announceForTest(t, h, store)
	baseURL := serveHub(t, h)
	oldClient := noRedirectClient(t)
	oldSecret, _ := completeWebSetup(t, oldClient, baseURL, install, "before", "the password I will forget")
	if session := authSession(t, oldClient, baseURL); session["authenticated"] != true {
		t.Fatalf("no session after the first setup: %v", session)
	}

	var out bytes.Buffer
	err = runLocalAuthCommandWithDependencies([]string{"local", "recover"}, nil, &out, localAuthCommandDependencies{
		newStore:         func() *hubauth.LocalStore { return store },
		openStore:        func() (*hubauth.LocalStore, error) { return store, nil },
		ensureOwner:      func() error { return nil },
		ensureSetupOwner: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("auth local recover: %v", err)
	}
	if strings.Contains(out.String(), "before") {
		t.Fatal("the recovery command revealed the username")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected recovery output:\n%s", out.String())
	}
	recoveryToken := strings.TrimSpace(lines[1])
	if session := authSession(t, noRedirectClient(t), baseURL); session["setup_available"] != true || session["setup_required"] != false {
		t.Fatalf("/v1/auth/session = %v; want a redeemable token on a configured hub", session)
	}

	newClient := noRedirectClient(t)
	newSecret, _ := completeWebSetup(t, newClient, baseURL, recoveryToken, "after", "a brand new password")
	if session := authSession(t, oldClient, baseURL); session["authenticated"] != false {
		t.Fatalf("an old session survived the recovery: %v", session)
	}
	response, _ := postJSON(t, noRedirectClient(t), baseURL+"/auth/local/login", map[string]string{"username": "before", "password": "the password I will forget", "totp": totpCode(t, oldSecret, time.Now())})
	if response.StatusCode == http.StatusOK {
		t.Fatal("the replaced credential still signs in")
	}
	response, payload := postJSON(t, noRedirectClient(t), baseURL+"/auth/local/login", map[string]string{"username": "after", "password": "a brand new password", "totp": totpCode(t, newSecret, time.Now())})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the recovered credential does not sign in: %d %s", response.StatusCode, payload)
	}
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(newSecret); err != nil {
		t.Fatalf("TOTP secret is not base32: %v", err)
	}
}

// A disabled credential with no OIDC is fail-closed too, but it gets no
// install token: only the explicit local recovery command can replace it.
func TestDisabledCredentialWithoutOIDCPointsAtRecovery(t *testing.T) {
	h, store, err := newSetupModeHub(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, _, err := store.Initialize("initialized then disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := announceSetup(h.auth, store, time.Minute, &out); err != nil {
		t.Fatal(err)
	}
	if installTokenLine.MatchString(out.String()) || !strings.Contains(out.String(), "auth local recover") {
		t.Fatalf("announcement for a disabled credential:\n%s", out.String())
	}
	if !h.auth.SetupRequired() {
		t.Fatal("a hub whose only credential is disabled is not fail-closed")
	}
}
