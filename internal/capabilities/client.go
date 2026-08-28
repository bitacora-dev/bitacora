package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ManifestPath is the hub endpoint that receives the capability manifest.
// It is deliberately separate from the telemetry ingest path (ADR-0008,
// POST /v1/ingest, Protobuf+zstd): the manifest is low-volume, control-plane
// data, and keeping it as plain JSON matches how ADR-0004 documents it and
// how the read-facing API is already handled (ADR-0008's JSON exception).
const ManifestPath = "/v1/manifest"

// Client sends capability manifests to the hub.
type Client struct {
	// BaseURL is the hub's address, e.g. "http://127.0.0.1:8081".
	BaseURL string
	// Token authenticates the agent (ADR-0008). Sent as a Bearer token.
	Token string

	HTTPClient *http.Client
}

// Send posts the manifest as JSON to BaseURL+ManifestPath. It returns an
// error if the hub is unreachable or responds with a non-2xx status; the
// caller decides whether that's fatal or just logged (the agent must keep
// collecting even if the hub is down).
func (c *Client) Send(ctx context.Context, m Manifest) error {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+ManifestPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building manifest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub rejected manifest: status %s", resp.Status)
	}
	return nil
}
