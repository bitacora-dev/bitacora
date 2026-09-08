package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/hubauth/oidctest"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// serveHub runs a hub on a loopback port and returns its base URL.
func serveHub(t *testing.T, h *hub) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: h2c.NewHandler(h.handler, &http2.Server{})}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})
	return "http://" + ln.Addr().String()
}

func enableOIDC(t *testing.T) *oidctest.Provider {
	t.Helper()
	provider, err := oidctest.New("bitacora-hub")
	if err != nil {
		t.Fatalf("starting fake provider: %v", err)
	}
	t.Cleanup(provider.Close)

	t.Setenv(hubauth.EnvIssuer, provider.Issuer())
	t.Setenv(hubauth.EnvClientID, "bitacora-hub")
	t.Setenv(hubauth.EnvClientSecret, "test-secret")
	t.Setenv(hubauth.EnvRedirectURL, "http://hub.example.invalid/auth/callback")
	return provider
}

// This is the regression that matters most in ADR-0019. Agents authenticate to
// /v1/ingest with the machine tokens of ADR-0008. If human authentication ever
// reaches that path, every agent goes silent at once while the hub keeps
// looking perfectly healthy — the same kind of silent blindness journald had.
func TestIngestKeepsWorkingWithHumanAuthEnabled(t *testing.T) {
	enableOIDC(t)

	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub with OIDC configured: %v", err)
	}
	defer h.Close()

	const hostID = "host-with-oidc"
	if err := h.tokens.AddToken(hostID, "machine-token"); err != nil {
		t.Fatalf("adding ingest token: %v", err)
	}

	baseURL := serveHub(t, h)
	client := &transport.Client{BaseURL: baseURL, Token: "machine-token"}
	if _, err := client.Send(context.Background(), &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
		LogLines: []*bitacorapb.LogLine{{
			TsMs:    time.Now().UnixMilli(),
			HostId:  hostID,
			Source:  "journald",
			Message: "agent still reporting with human auth on",
		}},
	}); err != nil {
		t.Fatalf("ingest broke with human authentication enabled: %v", err)
	}
}

// The companion of the test above: proving ingest survived is only meaningful
// if authentication was actually switched on.
func TestUIRedirectsToTheProviderWhenHumanAuthIsEnabled(t *testing.T) {
	enableOIDC(t)

	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub with OIDC configured: %v", err)
	}
	defer h.Close()

	baseURL := serveHub(t, h)
	req, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("requesting the UI: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("the UI answered %d without a session, want a redirect to the login", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location == "" {
		t.Fatal("the redirect carried no Location header")
	}
}

// Leaving the variables unset must change nothing: installations that put an
// identity proxy in front of the origin keep working exactly as before.
func TestUIStaysOpenWhenHumanAuthIsNotConfigured(t *testing.T) {
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub without OIDC: %v", err)
	}
	defer h.Close()

	baseURL := serveHub(t, h)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("requesting the UI: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound {
		t.Fatal("an unconfigured hub redirected to a login that does not exist")
	}
}
