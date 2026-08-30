package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// WebhookNotifier POSTs a JSON payload to an arbitrary URL — "integración
// con cualquier cosa, incluido Task Queue AI" (ADR-0009). The full Task
// Queue AI payload (timeline context, deep-link report) is a phase-4
// item per that ADR; this is the general-purpose webhook it's meant to
// eventually build on.
type WebhookNotifier struct {
	URL        string
	HTTPClient *http.Client
}

type webhookPayload struct {
	RuleID   string            `json:"rule_id"`
	Labels   map[string]string `json:"labels"`
	Severity string            `json:"severity"`
	State    string            `json:"state"`
	Value    float64           `json:"value"`
	At       string            `json:"at"`
	DeepLink string            `json:"deep_link,omitempty"`
}

// Notify implements Notifier.
func (w *WebhookNotifier) Notify(ctx context.Context, notif Notification) error {
	client := w.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	body, err := json.Marshal(webhookPayload{
		RuleID:   notif.RuleID,
		Labels:   notif.Labels,
		Severity: notif.Severity,
		State:    notif.State,
		Value:    notif.Value,
		At:       notif.At.Format("2006-01-02T15:04:05.000Z07:00"),
		DeepLink: notif.DeepLink,
	})
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint returned %s", resp.Status)
	}
	return nil
}
