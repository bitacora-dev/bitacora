package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookNotifier_SendsJSONPayload(t *testing.T) {
	var got webhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("unexpected error decoding payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &WebhookNotifier{URL: server.URL}
	n := sampleNotification()
	if err := notifier.Notify(context.Background(), n); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.RuleID != n.RuleID || got.Severity != n.Severity || got.State != n.State {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if got.Labels["host_id"] != "host-a" {
		t.Fatalf("expected labels to round-trip, got %+v", got.Labels)
	}
	if got.DeepLink != n.DeepLink {
		t.Fatalf("expected deep_link to round-trip, got %q", got.DeepLink)
	}
}

func TestWebhookNotifier_FailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	notifier := &WebhookNotifier{URL: server.URL}
	if err := notifier.Notify(context.Background(), sampleNotification()); err == nil {
		t.Fatal("expected an error for a 502 response")
	}
}
