package main

import (
	"crypto/hmac"
	"crypto/sha1" // RFC 6238's required default algorithm.
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
	"github.com/oklog/ulid/v2"
)

// totpCode recomputes RFC 6238 from the secret the CLI printed, which is what
// an authenticator application does. The hub must accept exactly this.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decoding TOTP secret: %v", err)
	}
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])) % 1_000_000
	return fmt.Sprintf("%06d", code)
}

func localAuthPaths(t *testing.T) hubauth.LocalConfig {
	t.Helper()
	dir := t.TempDir()
	return hubauth.LocalConfig{
		Required: true,
		Path:     filepath.Join(dir, "local-auth.json"),
		KeyPath:  filepath.Join(dir, "local-auth.key"),
	}
}

func authSession(t *testing.T, client *http.Client, baseURL string) map[string]any {
	t.Helper()
	response, err := client.Get(baseURL + "/v1/auth/session")
	if err != nil {
		t.Fatalf("GET /v1/auth/session: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/auth/session status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /v1/auth/session: %v", err)
	}
	return body
}

// Before this change the only way to turn local authentication on was to
// create its files, so a hub that had not been initialized served its
// dashboard to anyone. The switch has to bring the boundary up first.
func TestConfiguredLocalAuthGuardsAnUninitializedHub(t *testing.T) {
	cfg := localAuthPaths(t)
	store, err := hubauth.LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	h, err := newHubWithLocalAuth(t.TempDir(), store)
	if err != nil {
		t.Fatalf("newHubWithLocalAuth: %v", err)
	}
	defer h.Close()
	baseURL := serveHub(t, h)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("requesting the dashboard: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound || !strings.HasPrefix(response.Header.Get("Location"), "/auth/login") {
		t.Fatalf("dashboard status = %d, Location = %q; want a redirect to the login screen", response.StatusCode, response.Header.Get("Location"))
	}

	session := authSession(t, client, baseURL)
	if session["auth_enabled"] != true || session["local_pending_initialization"] != true || session["local_enabled"] != false {
		t.Fatalf("/v1/auth/session = %v; want an enabled boundary pending initialization", session)
	}

	// No request may create the operator account: ADR-0023 keeps that on the
	// server's TTY, and ADR-0025's tokenized web enrolment is not implemented.
	for _, attempt := range []struct {
		path, contentType, body string
		status                  int
	}{
		{"/auth/local/login", "application/json", `{"password":"first-visitor","totp":"000000"}`, http.StatusUnauthorized},
		{"/auth/local/login", "application/x-www-form-urlencoded", "password=first-visitor&totp=000000", http.StatusUnauthorized},
		// The login shell is a page, not a submission endpoint.
		{"/auth/login", "application/json", `{"password":"first-visitor","totp":"000000"}`, http.StatusMethodNotAllowed},
	} {
		response, err := client.Post(baseURL+attempt.path, attempt.contentType, strings.NewReader(attempt.body))
		if err != nil {
			t.Fatalf("POST %s: %v", attempt.path, err)
		}
		response.Body.Close()
		if response.StatusCode != attempt.status {
			t.Errorf("POST %s status = %d, want %d", attempt.path, response.StatusCode, attempt.status)
		}
		if cookies := response.Cookies(); len(cookies) > 0 {
			t.Errorf("POST %s handed out %d cookie(s)", attempt.path, len(cookies))
		}
	}
	for _, path := range []string{cfg.StatePath(), cfg.KeyStatePath()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stat %s err = %v; anonymous requests must not create a credential", path, err)
		}
	}
}

// The boundary must never reach the agent-facing ingest path, including while
// it is pending: an uninitialized hub that silences every agent would lose the
// observations the operator needs to diagnose it.
func TestConfiguredLocalAuthLeavesIngestReachableWhilePending(t *testing.T) {
	store, err := hubauth.LoadLocalStore(localAuthPaths(t))
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	h, err := newHubWithLocalAuth(t.TempDir(), store)
	if err != nil {
		t.Fatalf("newHubWithLocalAuth: %v", err)
	}
	defer h.Close()

	const hostID = "host-pending-local-auth"
	if err := h.tokens.AddToken(hostID, "machine-token"); err != nil {
		t.Fatalf("adding ingest token: %v", err)
	}
	client := &transport.Client{BaseURL: serveHub(t, h), Token: "machine-token"}
	if _, err := client.Send(t.Context(), &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
		LogLines: []*bitacorapb.LogLine{{
			TsMs:    time.Now().UnixMilli(),
			HostId:  hostID,
			Source:  "journald",
			Message: "pending local authentication must not silence the agent",
		}},
	}); err != nil {
		t.Fatalf("ingest with pending local authentication: %v", err)
	}
}

// The end-to-end path the owner follows after running the CLI: sign in with
// password and a real TOTP code, then read the dashboard with that session.
func TestInitializedLocalAuthSignsInWithTOTPAndOpensTheDashboard(t *testing.T) {
	// serveHub uses plaintext loopback HTTP. Make the test cookie transport
	// explicit rather than depending on the Go version's loopback Secure-cookie
	// handling; production keeps Secure cookies by default.
	t.Setenv(hubauth.EnvInsecureCookies, "true")

	cfg := localAuthPaths(t)
	secret, recovery, err := hubauth.NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := hubauth.LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	h, err := newHubWithLocalAuth(t.TempDir(), store)
	if err != nil {
		t.Fatalf("newHubWithLocalAuth: %v", err)
	}
	defer h.Close()
	baseURL := serveHub(t, h)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	if session := authSession(t, client, baseURL); session["local_enabled"] != true || session["local_pending_initialization"] != false {
		t.Fatalf("/v1/auth/session = %v; want an initialized local source", session)
	}

	body, err := json.Marshal(map[string]string{"password": "correct horse battery staple", "totp": totpCode(t, secret, time.Now())})
	if err != nil {
		t.Fatalf("marshalling login: %v", err)
	}
	response, err := client.Post(baseURL+"/auth/local/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /auth/local/login: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	session := authSession(t, client, baseURL)
	if session["authenticated"] != true {
		t.Fatalf("/v1/auth/session = %v; want an authenticated session", session)
	}
	identity, ok := session["identity"].(map[string]any)
	if !ok || identity["subject"] != "local:operator" || identity["source"] != "local" {
		t.Fatalf("identity = %v; want the ADR-0023 local:operator", session["identity"])
	}

	dashboard, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("requesting the dashboard: %v", err)
	}
	defer dashboard.Body.Close()
	if dashboard.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d, want %d", dashboard.StatusCode, http.StatusOK)
	}
	if len(recovery) != 10 {
		t.Fatalf("got %d recovery codes, want 10", len(recovery))
	}
}

// Five wrong attempts lock the subject, and the lockout surfaces through the
// login route the web UI reads as 429 with the unlock instant — not as a
// generic 401 that leaves a legitimate operator retrying a locked account.
func TestInitializedLocalAuthReportsLockoutThroughTheLoginRoute(t *testing.T) {
	cfg := localAuthPaths(t)
	if _, _, err := hubauth.NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := hubauth.LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	h, err := newHubWithLocalAuth(t.TempDir(), store)
	if err != nil {
		t.Fatalf("newHubWithLocalAuth: %v", err)
	}
	defer h.Close()
	baseURL := serveHub(t, h)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for attempt := 1; attempt <= 5; attempt++ {
		response, err := client.Post(baseURL+"/auth/local/login", "application/json", strings.NewReader(`{"password":"wrong","totp":"000000"}`))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		response.Body.Close()
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
	}

	// A correct credential is refused while the persisted lockout holds: the
	// 15 minutes are the defence, so they must not depend on guessing wrong.
	locked, err := client.Post(baseURL+"/auth/local/login", "application/json", strings.NewReader(`{"password":"correct horse battery staple","totp":"000000"}`))
	if err != nil {
		t.Fatalf("locked attempt: %v", err)
	}
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked attempt status = %d, want %d", locked.StatusCode, http.StatusTooManyRequests)
	}
	var body struct {
		LockedUntil string `json:"locked_until"`
	}
	if err := json.NewDecoder(locked.Body).Decode(&body); err != nil {
		t.Fatalf("decoding lockout: %v", err)
	}
	unlockAt, err := time.Parse(time.RFC3339, body.LockedUntil)
	if err != nil {
		t.Fatalf("locked_until = %q: %v", body.LockedUntil, err)
	}
	if remaining := time.Until(unlockAt); remaining <= 0 || remaining > 15*time.Minute {
		t.Fatalf("locked_until is %v away, want within the 15-minute ADR-0023 lockout", remaining)
	}
}

// The CLI that creates the credential and the server that reads it must
// resolve the same paths from the same configuration. Writing one credential
// and reading another is a silent failure: init reports success and the hub
// stays locked out.
func TestLocalAuthCLIUsesTheConfiguredPaths(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "local-auth.json")
	keyPath := filepath.Join(dir, "local-auth.key")
	t.Setenv(hubauth.EnvLocalAuthPath, statePath)
	t.Setenv(hubauth.EnvLocalAuthKeyPath, keyPath)

	// Nothing there yet: the CLI must say so rather than reach into /etc.
	var out strings.Builder
	if err := runLocalAuthCommand([]string{"local", "disable"}, nil, &out); !errors.Is(err, hubauth.ErrLocalAuthNotConfigured) {
		t.Fatalf("error = %v, want ErrLocalAuthNotConfigured", err)
	}

	if _, _, err := hubauth.NewLocalStore(statePath, keyPath).Initialize("correct horse battery staple"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Now the configured credential opens, and the command stops at the TTY
	// prompt — which is only reachable once the store was found.
	err := runLocalAuthCommand([]string{"local", "disable"}, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "TTY") {
		t.Fatalf("error = %v, want the TTY-only password prompt", err)
	}
}

func TestLocalAuthCLIRefusesAnUnparseableSwitch(t *testing.T) {
	t.Setenv(hubauth.EnvLocalAuth, "on-please")
	var out strings.Builder
	if err := runLocalAuthCommand([]string{"local", "init"}, nil, &out); err == nil || !strings.Contains(err.Error(), hubauth.EnvLocalAuth) {
		t.Fatalf("error = %v, want a complaint about %s", err, hubauth.EnvLocalAuth)
	}
}
