// Package hubauth implements the optional human authentication boundary of
// ADR-0019.
//
// The hub keeps an authentication boundary of its own but never becomes an
// identity provider: it stores no passwords, no hashes and no recovery flows.
// Identity comes from an OIDC provider the operator controls, so MFA, account
// recovery and account lifecycle stay with software built to maintain them.
//
// The whole package is optional. When Config.Enabled reports false the hub
// behaves exactly as it did before, which keeps working the installations that
// today put an identity proxy in front of the origin.
package hubauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// DefaultSessionTTL bounds how long a browser session survives without going
// back to the provider. It is deliberately short enough that revoking an
// account upstream takes effect the same day.
const DefaultSessionTTL = 12 * time.Hour

// pendingTTL bounds an in-flight login. A user who starts a login and never
// finishes it must not leave state behind for long.
const pendingTTL = 10 * time.Minute

const (
	sessionCookie = "bitacora_session"
	stateCookie   = "bitacora_auth_state"
)

// Config carries the operator-supplied OIDC settings. An empty Issuer,
// ClientID or RedirectURL disables human authentication entirely.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Scopes defaults to openid+profile+email when empty.
	Scopes []string
	// SessionTTL defaults to DefaultSessionTTL.
	SessionTTL time.Duration
	// InsecureCookies drops the Secure flag from the cookies. It exists for
	// local development over plain HTTP and must stay false in production.
	InsecureCookies bool
}

// Enabled reports whether the operator configured OIDC. Everything else in
// this package is inert while it returns false.
func (c Config) Enabled() bool {
	return c.Issuer != "" && c.ClientID != "" && c.RedirectURL != ""
}

// Identity is what the hub keeps about a signed-in person: enough to show who
// is looking and to write it into an audit trail, and nothing more.
type Identity struct {
	Subject string `json:"subject"`
	Email   string `json:"email,omitempty"`
	Name    string `json:"name,omitempty"`
}

type session struct {
	identity  Identity
	expiresAt time.Time
}

type pending struct {
	verifier  string
	nonce     string
	returnTo  string
	expiresAt time.Time
}

// Authenticator serves the login endpoints and answers whether a request
// carries a valid human session.
//
// Sessions live in memory: ADR-0003 keeps every layer embedded and a restart
// simply asks people to sign in again, which is a fair trade for not adding a
// session store to a single-maintainer product.
type Authenticator struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	ttl      time.Duration
	secure   bool

	mu       sync.Mutex
	sessions map[string]session
	pendings map[string]pending

	now func() time.Time
}

// New builds an Authenticator by discovering the provider metadata. It returns
// a nil Authenticator and no error when the config is disabled, so callers can
// wire it unconditionally.
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if !cfg.Enabled() {
		return nil, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering OIDC issuer %q: %w", cfg.Issuer, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}

	return &Authenticator{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		ttl:      ttl,
		secure:   !cfg.InsecureCookies,
		sessions: map[string]session{},
		pendings: map[string]pending{},
		now:      time.Now,
	}, nil
}

// Handler serves the authentication endpoints. They are mounted as exact
// paths so they stay reachable without a session.
func (a *Authenticator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", a.handleLogin)
	mux.HandleFunc("/auth/callback", a.handleCallback)
	mux.HandleFunc("/auth/logout", a.handleLogout)
	mux.HandleFunc("/auth/me", a.handleMe)
	return mux
}

// HasSession reports whether the request carries a live session. A nil
// Authenticator answers false, which is what keeps the guard inert when the
// operator did not configure OIDC — callers decide what that means.
func (a *Authenticator) HasSession(r *http.Request) bool {
	_, ok := a.Identity(r)
	return ok
}

// Identity returns the signed-in person behind a request, if any.
func (a *Authenticator) Identity(r *http.Request) (Identity, bool) {
	if a == nil {
		return Identity{}, false
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return Identity{}, false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[cookie.Value]
	if !ok {
		return Identity{}, false
	}
	if a.now().After(s.expiresAt) {
		delete(a.sessions, cookie.Value)
		return Identity{}, false
	}
	return s.identity, true
}

// RequireSession guards a handler with the human boundary. A nil
// Authenticator passes every request through untouched.
func (a *Authenticator) RequireSession(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.HasSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Browsers get sent to the provider; anything expecting data gets a
		// 401 it can act on instead of an HTML redirect it cannot parse.
		if !acceptsHTML(r) {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		a.startLogin(w, r, r.URL.RequestURI())
	})
}

func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	a.startLogin(w, r, r.URL.Query().Get("return_to"))
}

func (a *Authenticator) startLogin(w http.ResponseWriter, r *http.Request, returnTo string) {
	state, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	nonce, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start login")
		return
	}
	verifier := oauth2.GenerateVerifier()

	a.mu.Lock()
	a.sweepLocked()
	a.pendings[state] = pending{
		verifier:  verifier,
		nonce:     nonce,
		returnTo:  safeReturnTo(returnTo),
		expiresAt: a.now().Add(pendingTTL),
	}
	a.mu.Unlock()

	// The state also travels in a cookie so a callback carrying someone
	// else's state cannot complete a login in this browser.
	http.SetCookie(w, a.cookie(stateCookie, state, pendingTTL))
	url := a.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *Authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		writeJSONError(w, http.StatusUnauthorized, "provider rejected the login: "+errParam)
		return
	}

	state := r.URL.Query().Get("state")
	stateFromCookie, err := r.Cookie(stateCookie)
	if state == "" || err != nil || stateFromCookie.Value != state {
		writeJSONError(w, http.StatusBadRequest, "invalid authentication state")
		return
	}
	http.SetCookie(w, a.expiredCookie(stateCookie))

	a.mu.Lock()
	p, ok := a.pendings[state]
	delete(a.pendings, state)
	expired := ok && a.now().After(p.expiresAt)
	a.mu.Unlock()
	if !ok || expired {
		writeJSONError(w, http.StatusBadRequest, "invalid authentication state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeJSONError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	token, err := a.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(p.verifier))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "could not exchange the authorization code")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		writeJSONError(w, http.StatusUnauthorized, "provider returned no id_token")
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "could not verify the id_token")
		return
	}
	if idToken.Nonce != p.nonce {
		writeJSONError(w, http.StatusUnauthorized, "id_token nonce does not match")
		return
	}

	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	// Missing optional claims are not a failure: the subject is what
	// identifies the person, the rest is only there to show a name.
	_ = idToken.Claims(&claims)

	sid, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not create the session")
		return
	}
	a.mu.Lock()
	a.sweepLocked()
	a.sessions[sid] = session{
		identity:  Identity{Subject: idToken.Subject, Email: claims.Email, Name: claims.Name},
		expiresAt: a.now().Add(a.ttl),
	}
	a.mu.Unlock()

	http.SetCookie(w, a.cookie(sessionCookie, sid, a.ttl))
	http.Redirect(w, r, p.returnTo, http.StatusFound)
}

func (a *Authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, a.expiredCookie(sessionCookie))
	w.WriteHeader(http.StatusNoContent)
}

func (a *Authenticator) handleMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.Identity(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "no active session")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(identity)
}

// sweepLocked drops expired sessions and abandoned logins. It runs on the
// write paths so an idle hub does not keep a goroutine alive just to tidy up.
func (a *Authenticator) sweepLocked() {
	now := a.now()
	for id, s := range a.sessions {
		if now.After(s.expiresAt) {
			delete(a.sessions, id)
		}
	}
	for state, p := range a.pendings {
		if now.After(p.expiresAt) {
			delete(a.pendings, state)
		}
	}
}

func (a *Authenticator) cookie(name, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  a.now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	}
}

func (a *Authenticator) expiredCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// safeReturnTo keeps a login redirect inside this hub. An absolute URL in
// return_to would turn the login endpoint into an open redirect.
func safeReturnTo(candidate string) string {
	if candidate == "" || !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "//") {
		return "/"
	}
	return candidate
}

func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("could not read random bytes")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
