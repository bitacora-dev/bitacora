package hubauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const setupTestPassword = "a long enough setup password"

func TestSetupTokenIsSingleUseAndStoredOnlyAsAHash(t *testing.T) {
	store := newLocalStoreForTest(t)
	token, expiresAt, err := store.IssueSetupToken(SetupInstall, time.Minute)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("token %d characters long; want a 256-bit token", len(token))
	}
	if time.Until(expiresAt) > time.Minute || time.Until(expiresAt) <= 0 {
		t.Fatalf("expiresAt = %v; want within the TTL", expiresAt)
	}
	raw, err := os.ReadFile(store.SetupTokenPath())
	if err != nil {
		t.Fatalf("reading setup state: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("setup state contains the plaintext token")
	}
	info, err := os.Stat(store.SetupTokenPath())
	if err != nil || info.Mode().Perm() != localAuthFileMode {
		t.Fatalf("setup state mode = %v, err = %v; want %o", info.Mode().Perm(), err, localAuthFileMode)
	}
	if !store.SetupTokenPending() {
		t.Fatal("a fresh token is not reported as pending")
	}
	if _, err := store.CheckSetupToken("not-the-token"); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("CheckSetupToken(wrong) err = %v; want ErrSetupTokenInvalid", err)
	}

	secret, _, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	record, err := hashSecret([]byte(setupTestPassword))
	if err != nil {
		t.Fatal(err)
	}
	kind, recovery, generation, err := store.CompleteSetup(token, "operator", record, secret)
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	if kind != SetupInstall || len(recovery) != localAuthRecoveryCount || generation != 1 {
		t.Fatalf("CompleteSetup = %q, %d codes, generation %d", kind, len(recovery), generation)
	}
	if _, _, _, err := store.CompleteSetup(token, "operator", record, secret); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("second CompleteSetup err = %v; want ErrSetupTokenInvalid", err)
	}
	if store.SetupTokenPending() {
		t.Fatal("a consumed token is still reported as pending")
	}
	if !store.IsEnabled() {
		t.Fatal("setup did not enable the local credential")
	}
	if _, err := store.AuthenticateUserFromIP("operator", setupTestPassword, totpCode(secret, time.Now()), ""); err != nil {
		t.Fatalf("signing in with the set-up credential: %v", err)
	}
	if _, err := store.AuthenticateUserFromIP("someone-else", setupTestPassword, totpCode(secret, time.Now()), ""); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("signing in with the wrong username err = %v; want ErrInvalidLocalCredentials", err)
	}
	credential, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range append([]string{setupTestPassword, totpEncoding.EncodeToString(secret), token}, recovery...) {
		if strings.Contains(string(credential), plaintext) {
			t.Error("credential file contains a plaintext secret")
		}
	}
}

func TestSetupTokenExpires(t *testing.T) {
	store := newLocalStoreForTest(t)
	now := time.Now()
	store.now = func() time.Time { return now }
	token, _, err := store.IssueSetupToken(SetupInstall, 30*time.Minute)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	now = now.Add(29 * time.Minute)
	if _, err := store.CheckSetupToken(token); err != nil {
		t.Fatalf("token rejected before expiry: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := store.CheckSetupToken(token); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("expired token err = %v; want ErrSetupTokenInvalid", err)
	}
	if store.SetupTokenPending() {
		t.Fatal("an expired token is still reported as pending")
	}
}

func TestIssuingASetupTokenInvalidatesThePreviousOne(t *testing.T) {
	store := newLocalStoreForTest(t)
	first, _, err := store.IssueSetupToken(SetupInstall, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.IssueSetupToken(SetupRecovery, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckSetupToken(first); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("superseded token err = %v; want ErrSetupTokenInvalid", err)
	}
	if kind, err := store.CheckSetupToken(second); err != nil || kind != SetupRecovery {
		t.Fatalf("current token = %q, %v", kind, err)
	}
}

// An install token can never overwrite an existing credential: neither one
// issued after it exists nor one issued before someone ran `auth local init`.
func TestInstallTokenNeverOverwritesAnExistingCredential(t *testing.T) {
	store := newLocalStoreForTest(t)
	early, _, err := store.IssueSetupToken(SetupInstall, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Initialize("initialized from the CLI"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckSetupToken(early); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("install token after init err = %v; want ErrSetupTokenInvalid", err)
	}
	if _, _, err := store.IssueSetupToken(SetupInstall, time.Minute); err == nil {
		t.Fatal("issued an install token over an existing credential")
	}
}

// Total recovery replaces username, password, TOTP and recovery codes, bumps
// the generation and re-enables a disabled credential.
func TestRecoveryTokenReplacesTheWholeCredential(t *testing.T) {
	store := newLocalStoreForTest(t)
	_, oldRecovery, err := store.Initialize("the forgotten password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(); err != nil {
		t.Fatal(err)
	}
	before, err := store.readState()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.IssueSetupToken(SetupRecovery, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	record, err := hashSecret([]byte(setupTestPassword))
	if err != nil {
		t.Fatal(err)
	}
	kind, _, generation, err := store.CompleteSetup(token, "new-login", record, secret)
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	if kind != SetupRecovery || generation != before.SessionGeneration+1 {
		t.Fatalf("kind %q generation %d; want recovery and %d", kind, generation, before.SessionGeneration+1)
	}
	if !store.IsEnabled() {
		t.Fatal("recovery did not re-enable the credential")
	}
	if _, err := store.AuthenticateUserFromIP("new-login", "the forgotten password", totpCode(secret, time.Now()), ""); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("old password err = %v; want ErrInvalidLocalCredentials", err)
	}
	if _, err := store.AuthenticateUserFromIP("new-login", setupTestPassword, oldRecovery[0], ""); !errors.Is(err, ErrInvalidLocalCredentials) {
		t.Fatalf("old recovery code err = %v; want ErrInvalidLocalCredentials", err)
	}
	if _, err := store.AuthenticateUserFromIP("new-login", setupTestPassword, totpCode(secret, time.Now()), ""); err != nil {
		t.Fatalf("new credential: %v", err)
	}
}

func TestValidateSetupInput(t *testing.T) {
	for _, tc := range []struct {
		name, username, password string
		want                     error
	}{
		{"ok", " operator ", "twelve chars", nil},
		{"empty username", "  ", "a long enough password", ErrSetupInvalidUsername},
		{"control character", "op\x00", "a long enough password", ErrSetupInvalidUsername},
		{"too long username", strings.Repeat("a", 65), "a long enough password", ErrSetupInvalidUsername},
		{"short password", "operator", "elevenchars", ErrSetupPasswordTooShort},
		{"huge password", "operator", strings.Repeat("a", maxLocalPasswordBytes+1), ErrSetupPasswordTooLong},
		{"password is username", "operator-name", "OPERATOR-NAME", ErrSetupPasswordMatchesUsername},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateSetupInput(tc.username, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if err == nil && got != "operator" {
				t.Fatalf("username = %q, want trimmed", got)
			}
		})
	}
}

// The boundary built by NewBoundary always exists. With nothing configured it
// reports SetupRequired; once setup completes it stops doing so.
func TestNewBoundaryFailsClosedAndSetupOpensIt(t *testing.T) {
	store := newLocalStoreForTest(t)
	auth, err := NewBoundary(context.Background(), Config{InsecureCookies: true}, store)
	if err != nil || auth == nil {
		t.Fatalf("NewBoundary = %v, %v; want an authenticator", auth, err)
	}
	if !auth.SetupRequired() || auth.SetupAvailable() {
		t.Fatalf("SetupRequired = %v, SetupAvailable = %v; want true, false", auth.SetupRequired(), auth.SetupAvailable())
	}
	if _, err := NewBoundary(context.Background(), Config{}, nil); err == nil {
		t.Fatal("NewBoundary accepted a nil store")
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	token, _, err := store.IssueSetupToken(SetupInstall, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.SetupAvailable() {
		t.Fatal("SetupAvailable = false with a pending token")
	}
	handler := auth.Handler()
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("/auth/setup/begin", map[string]string{"token": "wrong", "username": "setup-user-name", "password": setupTestPassword}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("begin with a wrong token = %d", rec.Code)
	}
	if rec := post("/auth/setup/begin", map[string]string{"token": token, "username": "setup-user-name", "password": "short"}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "password_too_short") {
		t.Fatalf("begin with a short password = %d %s", rec.Code, rec.Body.String())
	}
	rec := post("/auth/setup/begin", map[string]string{"token": token, "username": "setup-user-name", "password": setupTestPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("begin = %d %s", rec.Code, rec.Body.String())
	}
	var begun setupBeginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(begun.OTPAuthURI, "otpauth://totp/") || !strings.Contains(begun.OTPAuthURI, "secret="+begun.TOTPSecret) {
		t.Fatalf("otpauth URI = %q", begun.OTPAuthURI)
	}
	if store.Initialized() {
		t.Fatal("begin persisted a credential before the TOTP code was confirmed")
	}
	secret, err := totpEncoding.DecodeString(begun.TOTPSecret)
	if err != nil {
		t.Fatal(err)
	}
	if rec := post("/auth/setup/complete", map[string]string{"token": token, "enrollment_id": begun.EnrollmentID, "totp": "abcdef"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("complete with a wrong code = %d", rec.Code)
	}
	if rec := post("/auth/setup/complete", map[string]string{"token": "wrong", "enrollment_id": begun.EnrollmentID, "totp": totpCode(secret, time.Now())}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("complete with a wrong token = %d", rec.Code)
	}
	rec = post("/auth/setup/complete", map[string]string{"token": token, "enrollment_id": begun.EnrollmentID, "totp": totpCode(secret, time.Now())})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete = %d %s", rec.Code, rec.Body.String())
	}
	var completed setupCompleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if len(completed.RecoveryCodes) != localAuthRecoveryCount {
		t.Fatalf("got %d recovery codes", len(completed.RecoveryCodes))
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("completing setup did not start a session")
	}
	if auth.SetupRequired() || auth.SetupAvailable() {
		t.Fatal("the boundary still reports setup after it completed")
	}
	if rec := post("/auth/setup/begin", map[string]string{"token": token, "username": "setup-user-name", "password": setupTestPassword}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reusing a burned token = %d", rec.Code)
	}

	for _, secretValue := range append([]string{token, setupTestPassword, begun.TOTPSecret, begun.EnrollmentID, "setup-user-name"}, completed.RecoveryCodes...) {
		if strings.Contains(logs.String(), secretValue) {
			t.Errorf("logs contain a secret or the username: %q", secretValue)
		}
	}
	if !strings.Contains(logs.String(), "local_setup_completed") {
		t.Errorf("the completed setup was not audited: %s", logs.String())
	}
}

func TestSetupCodeAttemptsAreBounded(t *testing.T) {
	store := newLocalStoreForTest(t)
	auth, err := NewBoundary(context.Background(), Config{}, store)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.IssueSetupToken(SetupInstall, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := auth.Handler()
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/auth/setup/begin", map[string]string{"token": token, "username": "operator", "password": setupTestPassword})
	var begun setupBeginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= setupMaxCodeAttempts; i++ {
		rec := post("/auth/setup/complete", map[string]string{"token": token, "enrollment_id": begun.EnrollmentID, "totp": "000000x"})
		if i < setupMaxCodeAttempts && rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d = %d", i, rec.Code)
		}
		if i == setupMaxCodeAttempts && rec.Code != http.StatusUnauthorized {
			t.Fatalf("last attempt = %d; want the enrolment discarded", rec.Code)
		}
	}
	secret, _ := totpEncoding.DecodeString(begun.TOTPSecret)
	if rec := post("/auth/setup/complete", map[string]string{"token": token, "enrollment_id": begun.EnrollmentID, "totp": totpCode(secret, time.Now())}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a discarded enrolment completed: %d", rec.Code)
	}
	if !store.SetupTokenPending() {
		t.Fatal("discarding the enrolment burned the token; the operator must be able to start again")
	}
	// Form posts are refused: a cross-site HTML form cannot send JSON.
	req := httptest.NewRequest(http.MethodPost, "/auth/setup/begin", strings.NewReader("token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form post = %d", rec.Code)
	}
}
