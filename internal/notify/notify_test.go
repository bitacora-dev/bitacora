package notify

import (
	"strings"
	"testing"
	"time"
)

func sampleNotification() Notification {
	return Notification{
		RuleID:   "cpu-temp-alta",
		Labels:   map[string]string{"host_id": "host-a"},
		Severity: "critical",
		State:    "firing",
		Value:    91.5,
		At:       time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC),
		DeepLink: "https://hub.example.invalid/?host_id=host-a",
	}
}

func TestNotification_TitleIncludesStateAndRule(t *testing.T) {
	title := sampleNotification().Title()
	if !strings.Contains(title, "FIRING") || !strings.Contains(title, "cpu-temp-alta") {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestNotification_TitleDefaultsToFiringWhenStateEmpty(t *testing.T) {
	n := sampleNotification()
	n.State = ""
	if !strings.Contains(n.Title(), "FIRING") {
		t.Fatalf("expected an empty state to default to FIRING, got %q", n.Title())
	}
}

func TestNotification_BodyIncludesDeepLink(t *testing.T) {
	body := sampleNotification().Body()
	if !strings.Contains(body, "https://hub.example.invalid/?host_id=host-a") {
		t.Fatalf("expected the deep link in the body, got %q", body)
	}
}

func TestDeepLink_BuildsURLWithHostAndTimestamp(t *testing.T) {
	at := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)
	link := DeepLink("https://hub.example.invalid", map[string]string{"host_id": "host-a"}, at)

	if !strings.Contains(link, "host_id=host-a") {
		t.Fatalf("expected host_id in the deep link, got %q", link)
	}
	if !strings.Contains(link, "at=2026-08-25T01") {
		t.Fatalf("expected the timestamp in the deep link, got %q", link)
	}
}

func TestDeepLink_EmptyWithoutBaseURLOrHostID(t *testing.T) {
	if got := DeepLink("", map[string]string{"host_id": "host-a"}, time.Now()); got != "" {
		t.Fatalf("expected empty deep link without a base URL, got %q", got)
	}
	if got := DeepLink("https://hub.example.invalid", map[string]string{}, time.Now()); got != "" {
		t.Fatalf("expected empty deep link without a host_id label, got %q", got)
	}
}
