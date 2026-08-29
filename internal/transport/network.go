package transport

import (
	"fmt"
	"net"
	"net/url"
)

// tailscaleCIDR is Tailscale's default CGNAT range for IPv4 addresses
// (100.64.0.0/10) — used to recognize a "we're on the tailnet" address
// without depending on the tailscale binary or its socket being present.
var tailscaleCIDR = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// IsTrustedNetwork reports whether ip is loopback or within Tailscale's
// address range — the two cases ADR-0008 says may use cleartext HTTP.
// Everything else requires TLS.
func IsTrustedNetwork(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || tailscaleCIDR.Contains(ip)
}

// ValidateTransportSecurity enforces ADR-0008's rule: the agent must
// refuse to connect in cleartext to a non-loopback, non-Tailscale address.
// host is a hostname or IP, without a port.
func ValidateTransportSecurity(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing URL: %w", err)
	}

	if u.Scheme == "https" {
		return nil // TLS is always acceptable, on or off the tailnet
	}
	if u.Scheme != "http" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		// A hostname, not a literal IP — resolve it. A DNS name that
		// doesn't resolve to a trusted-looking address is not
		// automatically safe just because it *could* be a Tailscale
		// MagicDNS name; require an explicit IP or https instead of
		// guessing from DNS, which an attacker controls before the
		// agent has a route to the real hub.
		return fmt.Errorf("cleartext HTTP requires a loopback or Tailscale IP literal, not a hostname (%q) — use https:// or an IP", host)
	}

	if !IsTrustedNetwork(ip) {
		return fmt.Errorf("refusing cleartext HTTP to %s: not loopback or Tailscale (100.64.0.0/10) — use https://", ip)
	}
	return nil
}

// ResolveBindAddr picks the address bitacora-hub's ingest listener binds
// to. ADR-0008/ADR-0002: never 0.0.0.0 by default — the operator must ask
// for that explicitly via allowPublic.
//
// If explicit is non-empty, it's used as given (still rejecting 0.0.0.0
// unless allowPublic). Otherwise: the first Tailscale-range IP found on a
// local interface, falling back to loopback if none is found.
func ResolveBindAddr(explicit string, allowPublic bool) (string, error) {
	if explicit != "" {
		if !allowPublic {
			ip := net.ParseIP(explicit)
			if ip != nil && ip.IsUnspecified() {
				return "", fmt.Errorf("refusing to bind %s: binding 0.0.0.0 is an explicit operator action, pass allowPublic", explicit)
			}
		}
		return explicit, nil
	}

	if ip := findTailscaleIP(); ip != "" {
		return ip, nil
	}
	return "127.0.0.1", nil
}

func findTailscaleIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if tailscaleCIDR.Contains(ipNet.IP) {
			return ipNet.IP.String()
		}
	}
	return ""
}
