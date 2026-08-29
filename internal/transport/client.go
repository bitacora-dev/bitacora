package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// Client is the agent side of POST /v1/ingest.
type Client struct {
	// BaseURL is the hub's ingest address, e.g. "http://127.0.0.1:8081"
	// (loopback/Tailscale, cleartext h2c) or "https://100.x.x.x:8081"
	// (anywhere else, per ADR-0008).
	BaseURL string
	Token   string

	// HTTPClient overrides the transport this Client uses. If nil, one is
	// built automatically: h2c for http://, standard (ALPN-negotiated
	// HTTP/2) for https://.
	HTTPClient *http.Client
}

// Send posts batch to BaseURL+"/v1/ingest" and returns the hub's response.
// It refuses to send over cleartext HTTP to anything but loopback or a
// Tailscale-range address (ADR-0008) before making any network call.
func (c *Client) Send(ctx context.Context, batch *bitacorapb.Batch) (*bitacorapb.IngestResponse, error) {
	if err := ValidateTransportSecurity(c.BaseURL); err != nil {
		return nil, fmt.Errorf("transport security check: %w", err)
	}

	raw, err := proto.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("marshaling batch: %w", err)
	}
	compressed, err := compressZstd(raw)
	if err != nil {
		return nil, fmt.Errorf("compressing batch: %w", err)
	}

	url := strings.TrimSuffix(c.BaseURL, "/") + "/v1/ingest"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/x-protobuf+zstd")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending batch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub rejected batch: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out bitacorapb.IngestResponse
	if err := proto.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &out, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	if strings.HasPrefix(c.BaseURL, "https://") {
		return &http.Client{} // net/http auto-negotiates HTTP/2 over TLS via ALPN
	}
	return newH2CClient()
}

// newH2CClient builds a client that speaks HTTP/2 in cleartext (h2c) — the
// standard pattern for talking to an h2c-only server without a prior
// HTTP/1.1 upgrade round trip.
func newH2CClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}
