package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNtfyNotifier_SendsTitlePriorityAndBody(t *testing.T) {
	var gotTitle, gotPriority, gotClick, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotClick = r.Header.Get("Click")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &NtfyNotifier{TopicURL: server.URL}
	if err := notifier.Notify(context.Background(), sampleNotification()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPriority != "urgent" {
		t.Fatalf("expected priority urgent for critical severity, got %q", gotPriority)
	}
	if gotClick != sampleNotification().DeepLink {
		t.Fatalf("expected Click header set to the deep link, got %q", gotClick)
	}
	if gotTitle == "" || gotBody == "" {
		t.Fatalf("expected non-empty title/body, got title=%q body=%q", gotTitle, gotBody)
	}
}

func TestNtfyNotifier_FailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := &NtfyNotifier{TopicURL: server.URL}
	if err := notifier.Notify(context.Background(), sampleNotification()); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestNtfyPriority_MapsEverySeverity(t *testing.T) {
	cases := map[string]string{
		"critical": "urgent",
		"error":    "high",
		"warn":     "default",
		"notice":   "low",
		"info":     "low",
		"debug":    "min",
		"":         "min",
	}
	for severity, want := range cases {
		if got := ntfyPriority(severity); got != want {
			t.Errorf("ntfyPriority(%q) = %q, want %q", severity, got, want)
		}
	}
}
