package hubauth

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/websecurity"
)

// setupEnrollmentTTL bounds the gap between proving the setup token and
// confirming the first TOTP code.
const (
	setupEnrollmentTTL   = 10 * time.Minute
	setupMaxCodeAttempts = 5
	totpIssuer           = "Bitácora"
)

// setupEnrollment is the in-memory half of a web setup: the password is
// already hashed and the TOTP secret is waiting for its first confirmed code.
// Only one exists at a time; starting again discards the previous one.
type setupEnrollment struct {
	id        string
	tokenHash string
	username  string
	password  passwordHash
	secret    []byte
	expiresAt time.Time
	attempts  int
}

// SetupRequired reports the ADR-0025 fail-closed state: there is no login
// source at all (no OIDC provider and no enabled local credential), so the hub
// serves only the setup screen. A nil Authenticator is the library-level
// "no boundary" mode and never requires setup.
func (a *Authenticator) SetupRequired() bool {
	return a != nil && a.provider == nil && !a.local.IsEnabled()
}

// SetupAvailable reports that a usable install or recovery token exists, so
// the setup screen has something to redeem.
func (a *Authenticator) SetupAvailable() bool {
	return a != nil && a.local.SetupTokenPending()
}

type setupBeginRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type setupBeginResponse struct {
	EnrollmentID string    `json:"enrollment_id"`
	TOTPSecret   string    `json:"totp_secret"`
	OTPAuthURI   string    `json:"otpauth_uri"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type setupCompleteRequest struct {
	Token        string `json:"token"`
	EnrollmentID string `json:"enrollment_id"`
	TOTP         string `json:"totp"`
}

type setupCompleteResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// handleSetupBegin is the first step of ADR-0025: it proves the setup token,
// validates and hashes the new password, and hands back a TOTP secret to
// enrol. Nothing is persisted and the token is not consumed yet.
func (a *Authenticator) handleSetupBegin(w http.ResponseWriter, r *http.Request) {
	if !a.setupRequestAllowed(w, r) {
		return
	}
	var body setupBeginRequest
	if err := decodeSetupJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid setup request")
		return
	}
	ip := websecurity.ClientIPString(r)
	kind, err := a.local.CheckSetupToken(body.Token)
	if err != nil {
		slog.Warn("authentication event", "event", "local_setup_token_rejected", "ip", ip)
		writeJSONError(w, http.StatusUnauthorized, "invalid_setup_token")
		return
	}
	if err := a.local.CheckSetupWritable(); err != nil {
		slog.Error("authentication event", "event", "local_setup_failed", "ip", ip, "reason", "state_not_writable", "error", err.Error())
		writeJSONError(w, http.StatusServiceUnavailable, "setup_state_read_only")
		return
	}
	username, err := ValidateSetupInput(body.Username, body.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": setupInputErrorCode(err), "min_password_length": MinLocalPasswordLength})
		return
	}
	password, err := hashSecret([]byte(body.Password))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not hash the password")
		return
	}
	secret, encoded, err := newTOTPSecret()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not create the TOTP secret")
		return
	}
	id, err := randomToken()
	if err != nil {
		zero(secret)
		writeJSONError(w, http.StatusInternalServerError, "could not start the setup")
		return
	}
	expiresAt := a.now().Add(setupEnrollmentTTL)
	a.mu.Lock()
	if a.enrollment != nil {
		zero(a.enrollment.secret)
	}
	a.enrollment = &setupEnrollment{
		id:        id,
		tokenHash: hashSetupToken(body.Token),
		username:  username,
		password:  password,
		secret:    secret,
		expiresAt: expiresAt,
	}
	a.mu.Unlock()
	slog.Info("authentication event", "event", "local_setup_started", "kind", string(kind), "subject", localOperatorSubject, "ip", ip)
	writeJSON(w, http.StatusOK, setupBeginResponse{
		EnrollmentID: id,
		TOTPSecret:   encoded,
		OTPAuthURI:   otpauthURI(username, encoded),
		ExpiresAt:    expiresAt.UTC(),
	})
}

// handleSetupComplete confirms the first TOTP code and redeems the token. Only
// then is the credential written and the token burned; the response carries
// the ten recovery codes, shown once, and a fresh session.
func (a *Authenticator) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	if !a.setupRequestAllowed(w, r) {
		return
	}
	var body setupCompleteRequest
	if err := decodeSetupJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid setup request")
		return
	}
	ip := websecurity.ClientIPString(r)

	a.mu.Lock()
	enrollment := a.enrollment
	valid := enrollment != nil &&
		a.now().Before(enrollment.expiresAt) &&
		subtle.ConstantTimeCompare([]byte(body.EnrollmentID), []byte(enrollment.id)) == 1 &&
		subtle.ConstantTimeCompare([]byte(hashSetupToken(body.Token)), []byte(enrollment.tokenHash)) == 1
	if !valid {
		a.mu.Unlock()
		slog.Warn("authentication event", "event", "local_setup_token_rejected", "ip", ip)
		writeJSONError(w, http.StatusUnauthorized, "invalid_setup_token")
		return
	}
	if !validTOTP(enrollment.secret, body.TOTP, a.now()) {
		enrollment.attempts++
		exhausted := enrollment.attempts >= setupMaxCodeAttempts
		if exhausted {
			zero(enrollment.secret)
			a.enrollment = nil
		}
		a.mu.Unlock()
		slog.Warn("authentication event", "event", "local_setup_code_rejected", "ip", ip, "restart_required", exhausted)
		if exhausted {
			writeJSONError(w, http.StatusUnauthorized, "setup_restart_required")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_code")
		return
	}
	// Claimed under the lock so two concurrent confirmations cannot both
	// redeem the same enrolment.
	a.enrollment = nil
	a.mu.Unlock()
	defer zero(enrollment.secret)

	kind, recovery, generation, err := a.local.CompleteSetup(body.Token, enrollment.username, enrollment.password, enrollment.secret)
	if err != nil {
		if errors.Is(err, ErrSetupTokenInvalid) {
			slog.Warn("authentication event", "event", "local_setup_token_rejected", "ip", ip)
			writeJSONError(w, http.StatusUnauthorized, "invalid_setup_token")
			return
		}
		if errors.Is(err, ErrSetupStateNotWritable) {
			// Checked before the token was consumed: fixing the mount and
			// starting again with the same token works.
			slog.Error("authentication event", "event", "local_setup_failed", "ip", ip, "reason", "state_not_writable", "error", err.Error())
			writeJSONError(w, http.StatusServiceUnavailable, "setup_state_read_only")
			return
		}
		slog.Error("authentication event", "event", "local_setup_failed", "ip", ip, "error", err.Error())
		writeJSONError(w, http.StatusInternalServerError, "setup_failed")
		return
	}

	sid, err := randomToken()
	a.mu.Lock()
	// Every earlier local session belongs to a credential that no longer
	// exists. The generation check would reject them anyway; dropping them
	// here makes the revocation immediate and unconditional.
	for id, s := range a.sessions {
		if s.localGeneration != nil {
			delete(a.sessions, id)
		}
	}
	if err == nil {
		a.sweepLocked()
		a.sessions[sid] = session{
			identity:        Identity{Source: sourceLocal, Subject: localOperatorSubject, Name: "Local operator"},
			expiresAt:       a.now().Add(DefaultSessionTTL),
			localGeneration: &generation,
		}
	}
	a.mu.Unlock()
	slog.Info("authentication event", "event", "local_setup_completed", "kind", string(kind), "subject", localOperatorSubject, "ip", ip)
	if err == nil {
		http.SetCookie(w, a.cookie(sessionCookie, sid, DefaultSessionTTL))
	}
	writeJSON(w, http.StatusOK, setupCompleteResponse{RecoveryCodes: recovery})
}

// setupRequestAllowed is shared by both steps: POST only, JSON only (a
// cross-site HTML form cannot send it without a CORS preflight the hub never
// answers), and a local store to write to.
func (a *Authenticator) setupRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if a.local == nil {
		writeJSONError(w, http.StatusNotFound, "local authentication is not configured")
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "setup requests must be JSON")
		return false
	}
	return true
}

func decodeSetupJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func setupInputErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrSetupInvalidUsername):
		return "invalid_username"
	case errors.Is(err, ErrSetupPasswordTooShort):
		return "password_too_short"
	case errors.Is(err, ErrSetupPasswordTooLong):
		return "password_too_long"
	case errors.Is(err, ErrSetupPasswordMatchesUsername):
		return "password_matches_username"
	default:
		return "invalid_setup_input"
	}
}

// otpauthURI follows the de facto Key Uri Format that authenticator apps scan.
func otpauthURI(username, secret string) string {
	label := url.PathEscape(totpIssuer + ":" + username)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", totpIssuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	return "otpauth://totp/" + label + "?" + query.Encode()
}
