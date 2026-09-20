package hubauth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newLocalStoreForTest(t *testing.T) *LocalStore {
	t.Helper()
	return NewLocalStore(filepath.Join(t.TempDir(), "local-auth.json"), filepath.Join(t.TempDir(), "local-auth.key"))
}

func TestLocalStoreInitializesEncryptedCredentialWithPrivateFiles(t *testing.T) {
	store := newLocalStoreForTest(t)
	secret, recovery, err := store.Initialize("correct horse battery staple")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(recovery) != localAuthRecoveryCount {
		t.Fatalf("got %d recovery codes, want %d", len(recovery), localAuthRecoveryCount)
	}
	state, err := store.readState()
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if salt, err := base64.RawStdEncoding.DecodeString(state.Password.Salt); err != nil || len(salt) != localAuthSaltBytes {
		t.Fatalf("password salt length = %d, err = %v; want %d bytes", len(salt), err, localAuthSaltBytes)
	}
	for _, path := range []string{store.path, store.keyPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != localAuthFileMode {
			t.Errorf("%s mode = %o, want %o", path, got, localAuthFileMode)
		}
	}
	bytes, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("reading credential: %v", err)
	}
	for _, plaintext := range append([]string{"correct horse battery staple", secret}, recovery...) {
		if strings.Contains(string(bytes), plaintext) {
			t.Errorf("credential file contains plaintext secret")
		}
	}
}

func TestLocalStoreAcceptsTOTPToleranceAndConsumesRecoveryCode(t *testing.T) {
	store := newLocalStoreForTest(t)
	secret, recovery, err := store.Initialize("password")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decoding TOTP secret: %v", err)
	}
	defer zero(key)
	for _, offset := range []int64{-1, 0, 1} {
		if _, err := store.Authenticate("password", totpCode(key, now.Add(time.Duration(offset)*totpPeriod))); err != nil {
			t.Errorf("TOTP offset %d was rejected: %v", offset, err)
		}
	}
	if _, err := store.Authenticate("password", totpCode(key, now.Add(2*totpPeriod))); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("TOTP outside tolerance error = %v, want invalid credentials", err)
	}
	if _, err := store.Authenticate("password", recovery[0]); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	if _, err := store.Authenticate("password", recovery[0]); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("used recovery code error = %v, want invalid credentials", err)
	}
	state, err := store.readState()
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if got := len(state.Recovery); got != localAuthRecoveryCount-1 {
		t.Fatalf("stored recovery codes = %d, want %d", got, localAuthRecoveryCount-1)
	}
}

func TestLocalStorePersistsLockoutAcrossReopen(t *testing.T) {
	store := newLocalStoreForTest(t)
	secret, _, err := store.Initialize("password")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	for range localAuthMaxFailures {
		if _, err := store.Authenticate("wrong", "000000"); !errors.Is(err, ErrInvalidLocalCredentials) {
			t.Fatalf("failed attempt: %v", err)
		}
	}
	reopened, err := OpenLocalStore(store.path, store.keyPath)
	if err != nil {
		t.Fatalf("OpenLocalStore: %v", err)
	}
	reopened.now = func() time.Time { return now }
	if _, err := reopened.Authenticate("password", secret); !errors.Is(err, ErrLocalAuthLocked) {
		t.Fatalf("reopened store error = %v, want lockout", err)
	}
}

func TestLocalStoreVerifiesCurrentPasswordWithoutCreatingASession(t *testing.T) {
	store := newLocalStoreForTest(t)
	if _, _, err := store.Initialize("current-password"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := store.VerifyPassword("current-password"); err != nil {
		t.Fatalf("VerifyPassword accepted current password with error: %v", err)
	}
	if err := store.VerifyPassword("wrong-password"); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("VerifyPassword wrong password error = %v, want invalid credentials", err)
	}
}

func TestLocalLoginUsesSharedSessionAndRotationRevokesIt(t *testing.T) {
	store := newLocalStoreForTest(t)
	secret, _, err := store.Initialize("password")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decoding TOTP secret: %v", err)
	}
	defer zero(key)
	auth, err := NewWithLocal(context.Background(), Config{InsecureCookies: true}, store)
	if err != nil {
		t.Fatalf("NewWithLocal: %v", err)
	}
	auth.now = func() time.Time { return now }
	body := []byte(`{"password":"password","totp":"` + totpCode(key, now) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	auth.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("local login status = %d, body = %s", resp.Code, resp.Body.String())
	}
	cookie := resp.Result().Cookies()[0]
	identityRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	identityRequest.AddCookie(cookie)
	identity, ok := auth.Identity(identityRequest)
	if !ok || identity.Source != "local" || identity.Subject != "local:operator" {
		t.Fatalf("identity = %#v, ok = %t", identity, ok)
	}
	if err := store.RotatePassword("rotated-password"); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}
	if _, ok := auth.Identity(identityRequest); ok {
		t.Fatal("credential rotation left a local session valid")
	}
	registration := httptest.NewRecorder()
	auth.Handler().ServeHTTP(registration, httptest.NewRequest(http.MethodGet, "/auth/local/register", nil))
	if registration.Code != http.StatusNotFound {
		t.Fatalf("registration route status = %d, want 404", registration.Code)
	}
}

func TestLocalLoginReportsLockoutUntilWithoutCredentialDetail(t *testing.T) {
	store := newLocalStoreForTest(t)
	if _, _, err := store.Initialize("password"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	auth, err := NewWithLocal(context.Background(), Config{InsecureCookies: true}, store)
	if err != nil {
		t.Fatalf("NewWithLocal: %v", err)
	}
	for range localAuthMaxFailures {
		req := httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(`{"password":"wrong","totp":"000000"}`))
		req.Header.Set("Content-Type", "application/json")
		auth.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(`{"password":"password","totp":"000000"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	auth.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding lockout response: %v", err)
	}
	if body["locked_until"] != now.Add(localAuthLockout).Format(time.RFC3339) {
		t.Fatalf("locked_until = %q", body["locked_until"])
	}
	if body["error"] == "invalid local credentials" {
		t.Fatal("lockout response hid the actionable lockout state")
	}
}

func TestLocalOperatorScopeModelsAllHostCapabilitiesWithoutEnforcement(t *testing.T) {
	if got := ScopeFor(Identity{Source: "local", Subject: "local:operator"}); got != (HostScope{AllHosts: true, View: true, Operate: true}) {
		t.Fatalf("local operator scope = %#v", got)
	}
	if got := ScopeFor(Identity{Source: "oidc", Subject: "operator"}); got != (HostScope{}) {
		t.Fatalf("OIDC scope = %#v, want no implicit local scope", got)
	}
}

func totpCode(secret []byte, at time.Time) string {
	counter := uint64(at.Unix() / int64(totpPeriod.Seconds()))
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])) % 1_000_000
	return fmt.Sprintf("%06d", code)
}
