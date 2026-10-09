// Package oidctest provides a minimal OIDC provider for tests.
//
// It exists as a normal package rather than a _test file because both the
// hubauth tests and the hub wiring tests need it, and the second of those is
// the regression that proves agent ingest still works with authentication on.
//
// It implements just enough of the specification to exercise the real client:
// discovery, a JWKS with an RSA key, an authorization endpoint that remembers
// the nonce, and a token endpoint that verifies the PKCE challenge and mints a
// signed id_token. Nothing here is meant for production use.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Provider is a fake OIDC issuer backed by an httptest server.
type Provider struct {
	Server   *httptest.Server
	ClientID string
	// Subject, Email and Name go into every id_token this provider mints.
	Subject string
	Email   string
	Name    string

	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]authRequest
	// LastChallenge records the PKCE challenge of the most recent
	// authorization request, so a test can assert the client sent one.
	LastChallenge string
}

type authRequest struct {
	nonce     string
	challenge string
}

// New starts a fake provider. The caller must Close it.
func New(clientID string) (*Provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating test key: %w", err)
	}
	p := &Provider{
		ClientID: clientID,
		Subject:  "test-subject",
		Email:    "operator@example.test",
		Name:     "Test Operator",
		key:      key,
		codes:    map[string]authRequest{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks", p.handleJWKS)
	mux.HandleFunc("/authorize", p.handleAuthorize)
	mux.HandleFunc("/token", p.handleToken)
	p.Server = httptest.NewServer(mux)
	return p, nil
}

// Close shuts the provider down.
func (p *Provider) Close() { p.Server.Close() }

// Issuer is the URL to configure the hub with.
func (p *Provider) Issuer() string { return p.Server.URL }

func (p *Provider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.Server.URL,
		"authorization_endpoint":                p.Server.URL + "/authorize",
		"token_endpoint":                        p.Server.URL + "/token",
		"jwks_uri":                              p.Server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
	})
}

func (p *Provider) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := p.key.Public().(*rsa.PublicKey)
	writeJSON(w, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": "test-key",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

// handleAuthorize records the nonce and PKCE challenge, then redirects back to
// the hub with an authorization code, the way a provider would after the
// person signs in.
func (p *Provider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())

	p.mu.Lock()
	p.codes[code] = authRequest{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	p.LastChallenge = q.Get("code_challenge")
	p.mu.Unlock()

	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	rq := redirect.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	redirect.RawQuery = rq.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")

	p.mu.Lock()
	req, ok := p.codes[code]
	delete(p.codes, code)
	p.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}

	// Verify PKCE the way a real provider does, so a client that forgets to
	// send the verifier fails here instead of passing the test.
	if req.challenge != "" {
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != req.challenge {
			http.Error(w, "pkce verification failed", http.StatusBadRequest)
			return
		}
	}

	idToken, err := p.signIDToken(req.nonce, time.Now().Add(time.Hour))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

// SignIDToken exposes token minting so a test can build an expired or
// otherwise invalid token on purpose.
func (p *Provider) SignIDToken(nonce string, expiry time.Time) (string, error) {
	return p.signIDToken(nonce, expiry)
}

func (p *Provider) signIDToken(nonce string, expiry time.Time) (string, error) {
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	claims := map[string]any{
		"iss":   p.Server.URL,
		"sub":   p.Subject,
		"aud":   p.ClientID,
		"exp":   expiry.Unix(),
		"iat":   time.Now().Unix(),
		"nonce": nonce,
		"email": p.Email,
		"name":  p.Name,
	}

	encode := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}
	h, err := encode(header)
	if err != nil {
		return "", err
	}
	c, err := encode(claims)
	if err != nil {
		return "", err
	}

	signingInput := h + "." + c
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// StripPort is a small helper for tests that need the issuer host.
func StripPort(rawURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "http://"), "https://")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
