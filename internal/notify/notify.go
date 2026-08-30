// Package notify implements ADR-0009's notifiers: ntfy (default),
// generic webhook, Telegram, SMTP, and the always-on system log. A
// Router dispatches a Notification to whichever notifiers match its
// severity/label filters, rate-limited per route.
package notify

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Notification is what a Notifier sends — built from an alerting.Alert
// plus enough deep-link context to jump straight to the relevant point
// in the timeline (ADR-0009: "toda alerta notificada incluye enlace
// profundo a la línea temporal centrada en su instante").
type Notification struct {
	RuleID   string
	Labels   map[string]string
	Severity string // schema.Severity value: debug/info/notice/warn/error/critical
	State    string // "firing" or "resolved"
	Value    float64
	At       time.Time
	DeepLink string
}

// Notifier sends one Notification through some channel.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Title renders a short, channel-agnostic summary line.
func (n Notification) Title() string {
	state := n.State
	if state == "" {
		state = "firing"
	}
	return fmt.Sprintf("[%s] %s", strings.ToUpper(state), n.RuleID)
}

// Body renders a longer, human-readable message including labels, value,
// and the deep link.
func (n Notification) Body() string {
	s := fmt.Sprintf("%s\nseverity: %s\nvalue: %v\nat: %s", n.Title(), n.Severity, n.Value, n.At.Format(time.RFC3339))
	for k, v := range n.Labels {
		s += fmt.Sprintf("\n%s: %s", k, v)
	}
	if n.DeepLink != "" {
		s += fmt.Sprintf("\n\n%s", n.DeepLink)
	}
	return s
}

// DeepLink builds a URL into the hub's single-page timeline
// (ADR-0014's GET /v1/summary?host_id=... contract, and the web UI that
// reads it), centered on at, for the host named in labels["host_id"].
// Returns "" if baseURL or the host_id label is empty — a notifier just
// omits the link rather than send a broken one.
func DeepLink(baseURL string, labels map[string]string, at time.Time) string {
	hostID := labels["host_id"]
	if baseURL == "" || hostID == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("host_id", hostID)
	q.Set("at", at.Format(time.RFC3339))
	u.RawQuery = q.Encode()
	return u.String()
}
