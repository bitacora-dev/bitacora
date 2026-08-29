package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MetadataClient reads container metadata through docker-socket-proxy —
// never the real Docker socket directly (ADR-0005: the agent is never in
// the docker group, since that's root-equivalent). The proxy is
// configured read-only over /containers, /info, /events, /version.
type MetadataClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewMetadataClient returns a client talking to the proxy at baseURL
// (e.g. "http://127.0.0.1:2375").
func NewMetadataClient(baseURL string) *MetadataClient {
	return &MetadataClient{
		BaseURL:    strings.TrimSuffix(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

type containerListEntry struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
}

// ContainerNames returns a map of full container ID to its primary name
// (the leading "/" Docker's API prefixes names with is stripped).
func (m *MetadataClient) ContainerNames(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.BaseURL+"/containers/json", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling docker-socket-proxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker-socket-proxy returned %s", resp.Status)
	}

	var entries []containerListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	names := make(map[string]string, len(entries))
	for _, e := range entries {
		if len(e.Names) == 0 {
			continue
		}
		names[e.ID] = strings.TrimPrefix(e.Names[0], "/")
	}
	return names, nil
}
