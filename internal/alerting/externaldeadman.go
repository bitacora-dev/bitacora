package alerting

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ExternalDeadman periodically pings an external service (ntfy, or a
// healthchecks.io-style endpoint hosted on the VPS — ADR-0009) so a
// hung or crashed hub is visible from *outside* iCloudServer. "Sin esto,
// un cuelgue del hub es indistinguible de 'todo va bien'."
type ExternalDeadman struct {
	URL        string
	HTTPClient *http.Client
}

// Ping sends one heartbeat. A non-2xx response or a transport error both
// count as failure — the caller decides what to do (log, retry on the
// next tick, etc.); Ping itself doesn't retry, since a monitoring
// heartbeat masking its own failures by retrying silently would defeat
// the point.
func (e *ExternalDeadman) Ping(ctx context.Context) error {
	client := e.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return fmt.Errorf("building heartbeat request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat endpoint returned %s", resp.Status)
	}
	return nil
}

// Run pings every interval until ctx is done. A failed ping is reported
// via onError (if non-nil) but never stops the loop — a single failure
// is exactly the kind of transient blip this mechanism must keep trying
// through, not give up on.
func (e *ExternalDeadman) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.Ping(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
