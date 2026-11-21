package websecurity

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tt := range []struct{ name, remote, forwarded, cloudflare, want string }{
		{"untrusted ignores spoofed headers", "192.0.2.1:80", "203.0.113.1", "198.51.100.4", "192.0.2.1"},
		{"trusted uses first forwarded client", "10.1.2.3:80", "203.0.113.1, 10.1.2.3", "", "203.0.113.1"},
		{"trusted cloudflare header wins", "10.1.2.3:80", "203.0.113.1", "198.51.100.4", "198.51.100.4"},
		{"ipv6 peer strips port", "[2001:db8::1]:443", "", "", "2001:db8::1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://hub/", nil)
			r.RemoteAddr = tt.remote
			r.Header.Set("X-Forwarded-For", tt.forwarded)
			r.Header.Set("CF-Connecting-IP", tt.cloudflare)
			if got := ClientIP(r, trusted).String(); got != tt.want {
				t.Fatalf("ClientIP() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMiddlewareOriginAndBearerRules(t *testing.T) {
	for _, tt := range []struct {
		name, origin, authorization string
		want                        int
	}{
		{"rejects cross origin session mutation", "https://evil.example", "", http.StatusForbidden},
		{"allows same origin session mutation", "https://hub", "", http.StatusNoContent},
		{"rejects missing origin session mutation", "", "", http.StatusForbidden},
		{"allows bearer request without origin", "", "Bearer device-token", http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), Config{}, func(*http.Request) bool { return true })
			r := httptest.NewRequest(http.MethodPost, "http://hub/v1/hosts", nil)
			r.Host = "hub"
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Authorization", tt.authorization)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestMiddlewareRateLimitAndSecurityHeaders(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), Config{LoginRate: 1, LoginBurst: 1, PairRate: 1, PairBurst: 1}, nil)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://hub/auth/local/login", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := request(); got.Code != http.StatusNoContent || got.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("first login status/HSTS = %d/%q", got.Code, got.Header().Get("Strict-Transport-Security"))
	}
	if got := request(); got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("limited login status/retry = %d/%q", got.Code, got.Header().Get("Retry-After"))
	}
}

// The ADR-0025 setup steps take a secret without a session, so they share the
// login budget instead of getting an unthrottled path of their own.
func TestMiddlewareRateLimitsTheSetupSteps(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), Config{LoginRate: 1, LoginBurst: 1, PairRate: 1, PairBurst: 1}, nil)
	for _, path := range []string{"/auth/setup/begin", "/auth/setup/complete"} {
		t.Run(path, func(t *testing.T) {
			request := func() int {
				r := httptest.NewRequest(http.MethodPost, "https://hub"+path, nil)
				r.RemoteAddr = "192.0.2.10:1234"
				if path == "/auth/setup/complete" {
					r.RemoteAddr = "192.0.2.11:1234"
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w.Code
			}
			if got := request(); got != http.StatusNoContent {
				t.Fatalf("first request = %d", got)
			}
			if got := request(); got != http.StatusTooManyRequests {
				t.Fatalf("second request = %d, want 429", got)
			}
		})
	}
}
