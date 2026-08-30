package transport

import "testing"

func TestValidateTransportSecurity_AllowsHTTPSAnywhere(t *testing.T) {
	if err := ValidateTransportSecurity("https://example.invalid:8081"); err != nil {
		t.Fatalf("expected https to always be allowed, got %v", err)
	}
}

func TestValidateTransportSecurity_AllowsCleartextLoopback(t *testing.T) {
	if err := ValidateTransportSecurity("http://127.0.0.1:8081"); err != nil {
		t.Fatalf("expected cleartext loopback to be allowed, got %v", err)
	}
}

func TestValidateTransportSecurity_AllowsCleartextTailscaleRange(t *testing.T) {
	if err := ValidateTransportSecurity("http://100.64.1.2:8081"); err != nil {
		t.Fatalf("expected cleartext Tailscale-range IP to be allowed, got %v", err)
	}
}

func TestValidateTransportSecurity_RejectsCleartextPublicIP(t *testing.T) {
	if err := ValidateTransportSecurity("http://203.0.113.5:8081"); err == nil {
		t.Fatal("expected cleartext HTTP to a public IP to be rejected")
	}
}

func TestValidateTransportSecurity_RejectsCleartextHostname(t *testing.T) {
	if err := ValidateTransportSecurity("http://hub.example.invalid:8081"); err == nil {
		t.Fatal("expected cleartext HTTP to a hostname to be rejected — can't verify trust from a name alone")
	}
}

func TestResolveBindAddr_ExplicitAddrIsUsed(t *testing.T) {
	addr, err := ResolveBindAddr("100.64.1.2:8081", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "100.64.1.2:8081" {
		t.Fatalf("expected the explicit address to be used verbatim, got %q", addr)
	}
}

func TestResolveBindAddr_RejectsUnspecifiedWithoutAllowPublic(t *testing.T) {
	if _, err := ResolveBindAddr("0.0.0.0", false); err == nil {
		t.Fatal("expected binding 0.0.0.0 without allowPublic to be rejected")
	}
}

func TestResolveBindAddr_AllowsUnspecifiedWithAllowPublic(t *testing.T) {
	addr, err := ResolveBindAddr("0.0.0.0", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "0.0.0.0" {
		t.Fatalf("expected 0.0.0.0 to be returned when explicitly allowed, got %q", addr)
	}
}

func TestResolveBindAddr_DefaultsToLoopbackWithoutTailscale(t *testing.T) {
	// This test environment (CI runner, dev laptop) has no tailscale0
	// interface, so the fallback path is what's actually exercised.
	addr, err := ResolveBindAddr("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr == "" {
		t.Fatal("expected a non-empty default bind address")
	}
}
