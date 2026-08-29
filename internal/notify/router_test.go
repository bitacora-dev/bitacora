package notify

import (
	"context"
	"errors"
	"testing"
)

type recordingNotifier struct {
	calls []Notification
	err   error
}

func (r *recordingNotifier) Notify(ctx context.Context, n Notification) error {
	r.calls = append(r.calls, n)
	return r.err
}

func TestRouter_RoutesBySeverity(t *testing.T) {
	ntfy := &recordingNotifier{}
	telegram := &recordingNotifier{}
	logNotifier := &recordingNotifier{}

	// ADR-0009's own example routing table.
	router := NewRouter([]Route{
		{Name: "ntfy", Notifier: ntfy, Severities: []string{"critical", "warn"}},
		{Name: "telegram", Notifier: telegram, Severities: []string{"critical"}},
		{Name: "log", Notifier: logNotifier}, // no filter: everything
	}, 0, 0)

	warnNotif := sampleNotification()
	warnNotif.Severity = "warn"
	router.Dispatch(context.Background(), warnNotif)

	if len(ntfy.calls) != 1 {
		t.Fatalf("expected warn to reach ntfy, got %d calls", len(ntfy.calls))
	}
	if len(telegram.calls) != 0 {
		t.Fatalf("expected warn NOT to reach telegram (critical-only route), got %d calls", len(telegram.calls))
	}
	if len(logNotifier.calls) != 1 {
		t.Fatalf("expected warn to reach the unfiltered log route, got %d calls", len(logNotifier.calls))
	}

	criticalNotif := sampleNotification()
	criticalNotif.Severity = "critical"
	router.Dispatch(context.Background(), criticalNotif)

	if len(ntfy.calls) != 2 || len(telegram.calls) != 1 {
		t.Fatalf("expected critical to reach both ntfy and telegram, got ntfy=%d telegram=%d", len(ntfy.calls), len(telegram.calls))
	}
}

func TestRouter_RoutesByLabel(t *testing.T) {
	prod := &recordingNotifier{}
	router := NewRouter([]Route{
		{Name: "prod-only", Notifier: prod, Labels: map[string]string{"env": "prod"}},
	}, 0, 0)

	devNotif := sampleNotification()
	devNotif.Labels = map[string]string{"host_id": "host-a", "env": "dev"}
	router.Dispatch(context.Background(), devNotif)
	if len(prod.calls) != 0 {
		t.Fatalf("expected a dev-labeled notification not to reach the prod-only route, got %d calls", len(prod.calls))
	}

	prodNotif := sampleNotification()
	prodNotif.Labels = map[string]string{"host_id": "host-a", "env": "prod"}
	router.Dispatch(context.Background(), prodNotif)
	if len(prod.calls) != 1 {
		t.Fatalf("expected a prod-labeled notification to reach the prod-only route, got %d calls", len(prod.calls))
	}
}

func TestRouter_OneRouteFailingDoesNotStopOthers(t *testing.T) {
	failing := &recordingNotifier{err: errors.New("unreachable")}
	working := &recordingNotifier{}

	router := NewRouter([]Route{
		{Name: "failing", Notifier: failing},
		{Name: "working", Notifier: working},
	}, 0, 0)

	errs := router.Dispatch(context.Background(), sampleNotification())

	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(errs), errs)
	}
	if len(working.calls) != 1 {
		t.Fatal("expected the working route to still receive the notification despite the other one failing")
	}
}

func TestRouter_RateLimitsAcrossRoutes(t *testing.T) {
	notifier := &recordingNotifier{}
	// burst=1: the first Dispatch call succeeds, immediate retries don't.
	router := NewRouter([]Route{{Name: "r", Notifier: notifier}}, 0.001, 1)

	router.Dispatch(context.Background(), sampleNotification())
	errs := router.Dispatch(context.Background(), sampleNotification())

	if len(notifier.calls) != 1 {
		t.Fatalf("expected exactly 1 call to have gone through before the rate limit, got %d", len(notifier.calls))
	}
	if len(errs) != 1 {
		t.Fatalf("expected the second dispatch to report a rate-limit error, got %v", errs)
	}
}

func TestRouter_UnlimitedWhenNoRateGiven(t *testing.T) {
	notifier := &recordingNotifier{}
	router := NewRouter([]Route{{Name: "r", Notifier: notifier}}, 0, 0)

	for i := 0; i < 10; i++ {
		router.Dispatch(context.Background(), sampleNotification())
	}
	if len(notifier.calls) != 10 {
		t.Fatalf("expected all 10 dispatches to go through unlimited, got %d", len(notifier.calls))
	}
}
