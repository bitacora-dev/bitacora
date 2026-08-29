package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// NtfyNotifier sends to a ntfy topic (ADR-0009's default notifier) — self-
// hosted, with native iOS/Android apps that solve mobile push without this
// project touching APNs (ADR-0014).
type NtfyNotifier struct {
	// TopicURL is the full topic address, e.g.
	// "https://ntfy.sh/my-topic" or a self-hosted instance's equivalent.
	TopicURL   string
	HTTPClient *http.Client
}

// Notify implements Notifier.
func (n *NtfyNotifier) Notify(ctx context.Context, notif Notification) error {
	client := n.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.TopicURL, strings.NewReader(notif.Body()))
	if err != nil {
		return fmt.Errorf("building ntfy request: %w", err)
	}
	req.Header.Set("Title", notif.Title())
	req.Header.Set("Priority", ntfyPriority(notif.Severity))
	if notif.DeepLink != "" {
		req.Header.Set("Click", notif.DeepLink)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending to ntfy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %s", resp.Status)
	}
	return nil
}

// ntfyPriority maps a schema.Severity value to ntfy's priority levels
// (https://docs.ntfy.sh/publish/#message-priority): 1 min .. 5 urgent.
func ntfyPriority(severity string) string {
	switch severity {
	case "critical":
		return "urgent"
	case "error":
		return "high"
	case "warn":
		return "default"
	case "notice", "info":
		return "low"
	default: // debug, or anything unrecognized
		return "min"
	}
}
