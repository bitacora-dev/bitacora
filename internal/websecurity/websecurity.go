// Package websecurity provides HTTP boundary protections for the hub.
package websecurity

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultLoginRate  = 5
	defaultLoginBurst = 5
	defaultPairRate   = 10
	defaultPairBurst  = 10
)

// Config is deliberately explicit: forwarding headers are untrusted unless
// the immediate peer belongs to TrustedProxies.
type Config struct {
	TrustedProxies []netip.Prefix
	LoginRate      rate.Limit
	LoginBurst     int
	PairRate       rate.Limit
	PairBurst      int
}

// ConfigFromEnv reads the hub's reverse-proxy and request-rate settings.
// BITACORA_TRUSTED_PROXY_CIDRS defaults to no trusted proxies, so client
// supplied forwarding headers are never accepted by default. Rate variables
// are requests per minute: BITACORA_LOGIN_RATE_PER_MINUTE,
// BITACORA_LOGIN_RATE_BURST, BITACORA_PAIR_RATE_PER_MINUTE and
// BITACORA_PAIR_RATE_BURST.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		LoginRate:  rate.Limit(float64(defaultLoginRate) / 60),
		LoginBurst: defaultLoginBurst,
		PairRate:   rate.Limit(float64(defaultPairRate) / 60),
		PairBurst:  defaultPairBurst,
	}
	for _, raw := range strings.Split(os.Getenv("BITACORA_TRUSTED_PROXY_CIDRS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parsing BITACORA_TRUSTED_PROXY_CIDRS: %w", err)
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, prefix)
	}
	var err error
	if cfg.LoginRate, err = rateFromEnv("BITACORA_LOGIN_RATE_PER_MINUTE", cfg.LoginRate); err != nil {
		return Config{}, err
	}
	if cfg.LoginBurst, err = positiveIntFromEnv("BITACORA_LOGIN_RATE_BURST", cfg.LoginBurst); err != nil {
		return Config{}, err
	}
	if cfg.PairRate, err = rateFromEnv("BITACORA_PAIR_RATE_PER_MINUTE", cfg.PairRate); err != nil {
		return Config{}, err
	}
	if cfg.PairBurst, err = positiveIntFromEnv("BITACORA_PAIR_RATE_BURST", cfg.PairBurst); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func rateFromEnv(name string, fallback rate.Limit) (rate.Limit, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of requests per minute", name)
	}
	return rate.Limit(value / 60), nil
}

func positiveIntFromEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

// ClientIP uses the socket peer unless that peer is explicitly trusted. A
// trusted Cloudflare/reverse-proxy peer may supply CF-Connecting-IP or XFF.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer, err := peerAddr(r.RemoteAddr)
	if err != nil || !isTrusted(peer, trusted) {
		return peer
	}
	if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
		return cf.Unmap()
	}
	for _, raw := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if ip, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
			return ip.Unmap()
		}
	}
	return peer
}

func peerAddr(raw string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return ip.Unmap(), nil
}

func isTrusted(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

type clientIPContextKey struct{}

// ClientIPString returns the resolved peer identity inserted by Middleware.
// It deliberately returns an empty value for handlers served without it.
func ClientIPString(r *http.Request) string {
	value, _ := r.Context().Value(clientIPContextKey{}).(string)
	return value
}

type perIPLimiter struct {
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
	limiters map[netip.Addr]*rate.Limiter
}

func newPerIPLimiter(r rate.Limit, burst int) *perIPLimiter {
	if r <= 0 {
		r = rate.Limit(float64(defaultLoginRate) / 60)
	}
	if burst <= 0 {
		burst = defaultLoginBurst
	}
	return &perIPLimiter{rate: r, burst: burst, limiters: make(map[netip.Addr]*rate.Limiter)}
}

func (l *perIPLimiter) allow(ip netip.Addr, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	limiter := l.limiters[ip]
	if limiter == nil {
		limiter = rate.NewLimiter(l.rate, l.burst)
		l.limiters[ip] = limiter
	}
	reservation := limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return false, time.Minute
	}
	delay := reservation.DelayFrom(now)
	if delay == 0 {
		return true, 0
	}
	reservation.CancelAt(now)
	return false, delay
}

// Middleware applies response headers, cache protection, login/pairing
// throttles, and an Origin/Referer check only to cookie-authenticated writes.
func Middleware(next http.Handler, cfg Config, hasSession func(*http.Request) bool) http.Handler {
	loginLimiter := newPerIPLimiter(cfg.LoginRate, cfg.LoginBurst)
	pairLimiter := newPerIPLimiter(cfg.PairRate, cfg.PairBurst)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r, cfg.TrustedProxies)
		r = r.WithContext(context.WithValue(r.Context(), clientIPContextKey{}, ip.String()))
		secure := r.TLS != nil || (requestFromTrustedProxy(r, cfg.TrustedProxies) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
		securityHeaders(w, secure)
		if strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Cache-Control", "no-store")
		}

		var limiter *perIPLimiter
		switch r.URL.Path {
		case "/auth/local/login", "/auth/login":
			if r.Method == http.MethodPost {
				limiter = loginLimiter
			}
		case "/v1/devices/claim", "/v1/devices/pair":
			if r.Method == http.MethodPost {
				limiter = pairLimiter
			}
		}
		if limiter != nil {
			if ok, retry := limiter.allow(ip, time.Now()); !ok {
				w.Header().Set("Retry-After", retryAfter(retry))
				slog.Warn("authentication event", "event", "auth_rate_limit", "ip", ip.String(), "path", r.URL.Path)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		if mutation(r.Method) && !hasBearerToken(r) && hasSession != nil && hasSession(r) && !sameOrigin(r, cfg.TrustedProxies) {
			slog.Warn("authentication event", "event", "auth_cross_origin_rejected", "ip", ip.String(), "path", r.URL.Path)
			http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func retryAfter(delay time.Duration) string {
	seconds := int(delay.Seconds())
	if delay%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func mutation(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func hasBearerToken(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

func requestFromTrustedProxy(r *http.Request, trusted []netip.Prefix) bool {
	ip, err := peerAddr(r.RemoteAddr)
	return err == nil && isTrusted(ip, trusted)
}

func sameOrigin(r *http.Request, trusted []netip.Prefix) bool {
	reference := r.Header.Get("Origin")
	if reference == "" {
		reference = r.Referer()
	}
	if reference == "" {
		return false
	}
	u, err := url.Parse(reference)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := r.Host
	if requestFromTrustedProxy(r, trusted) {
		if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		}
	}
	return strings.EqualFold(u.Host, host)
}

func securityHeaders(w http.ResponseWriter, secure bool) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
	if secure {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}
