package hubauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pendingLocalAuthenticator(t *testing.T) (*Authenticator, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := LoadLocalStore(LocalConfig{
		Required: true,
		Path:     filepath.Join(dir, "local-auth.json"),
		KeyPath:  filepath.Join(dir, "local-auth.key"),
	})
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	auth, err := NewWithLocal(context.Background(), Config{}, store)
	if err != nil {
		t.Fatalf("NewWithLocal: %v", err)
	}
	if auth == nil {
		t.Fatal("a configured local source must produce a live boundary before initialization")
	}
	return auth, dir
}

// The security requirement: between switching local authentication on and
// running the CLI, the boundary is up and nothing an anonymous visitor can
// send creates the operator credential.
func TestPendingLocalAuthenticationGuardsEverythingAndClaimsNothing(t *testing.T) {
	auth, dir := pendingLocalAuthenticator(t)

	guarded := auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an uninitialized hub served a guarded route")
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Errorf("guarded page status = %d, want %d", recorder.Code, http.StatusFound)
	}

	// Every shape of the only local write path, including the ones a first
	// visitor would reach for if the flow allowed self-enrolment.
	attempts := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(`{"password":"first-visitor","totp":"000000"}`)),
		httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(`{"password":"first-visitor","recovery_code":"anything"}`)),
		httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader("password=first-visitor&totp=000000")),
	}
	attempts[0].Header.Set("Content-Type", "application/json")
	attempts[1].Header.Set("Content-Type", "application/json")
	attempts[2].Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, attempt := range attempts {
		recorder := httptest.NewRecorder()
		auth.Handler().ServeHTTP(recorder, attempt)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want %d", attempt.Header.Get("Content-Type"), recorder.Code, http.StatusUnauthorized)
		}
		if cookies := recorder.Result().Cookies(); len(cookies) > 0 {
			t.Errorf("an uninitialized hub handed out %d cookie(s)", len(cookies))
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading credential directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("anonymous requests created %d credential file(s)", len(entries))
	}
}

func TestPendingLocalAuthenticationIsReportedWithoutOfferingALoginForm(t *testing.T) {
	auth, _ := pendingLocalAuthenticator(t)
	if !auth.LocalPendingInitialization() {
		t.Error("LocalPendingInitialization() = false, want true")
	}
	if local, oidc := auth.LoginSources(); local || oidc {
		t.Errorf("LoginSources() = (%t, %t); an uninitialized credential offers no usable route", local, oidc)
	}

	recorder := httptest.NewRecorder()
	auth.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/me", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("/auth/me status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var body struct {
		LocalEnabled               bool `json:"local_enabled"`
		LocalPendingInitialization bool `json:"local_pending_initialization"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /auth/me: %v", err)
	}
	if body.LocalEnabled || !body.LocalPendingInitialization {
		t.Errorf("/auth/me reported local_enabled=%t pending=%t; want false, true", body.LocalEnabled, body.LocalPendingInitialization)
	}
}

func TestInitializedLocalCredentialStopsReportingAsPending(t *testing.T) {
	dir := t.TempDir()
	cfg := LocalConfig{Required: true, Path: filepath.Join(dir, "local-auth.json"), KeyPath: filepath.Join(dir, "local-auth.key")}
	if _, _, err := NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	auth, err := NewWithLocal(context.Background(), Config{}, store)
	if err != nil {
		t.Fatalf("NewWithLocal: %v", err)
	}
	if auth.LocalPendingInitialization() {
		t.Error("LocalPendingInitialization() = true after init")
	}
	if local, _ := auth.LoginSources(); !local {
		t.Error("LoginSources() reported no local route for an initialized credential")
	}
}

// The documented recovery path after the deliberate lockout: wait it out, then
// use one of the ten single-use recovery codes in place of TOTP.
func TestLockoutExpiresAndThenARecoveryCodeSignsInExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	cfg := LocalConfig{Required: true, Path: filepath.Join(dir, "local-auth.json"), KeyPath: filepath.Join(dir, "local-auth.key")}
	_, recovery, err := NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }

	for attempt := 1; attempt <= localAuthMaxFailures; attempt++ {
		if _, err := store.Authenticate("wrong", "000000"); !errors.Is(err, ErrInvalidLocalCredentials) {
			t.Fatalf("attempt %d error = %v, want invalid credentials", attempt, err)
		}
	}
	if _, err := store.Authenticate("correct horse battery staple", recovery[0]); !errors.Is(err, ErrLocalAuthLocked) {
		t.Fatalf("locked attempt error = %v, want locked", err)
	}
	if lockedUntil, err := store.LockedUntil(); err != nil || !lockedUntil.Equal(now.Add(localAuthLockout)) {
		t.Fatalf("LockedUntil() = %v, %v; want %v", lockedUntil, err, now.Add(localAuthLockout))
	}

	now = now.Add(localAuthLockout + time.Minute)
	if _, err := store.Authenticate("correct horse battery staple", recovery[0]); err != nil {
		t.Fatalf("recovery code after the lockout: %v", err)
	}
	if _, err := store.Authenticate("correct horse battery staple", recovery[0]); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("reused recovery code error = %v, want invalid credentials", err)
	}
	if _, err := store.Authenticate("correct horse battery staple", recovery[1]); err != nil {
		t.Fatalf("second recovery code: %v", err)
	}
}
