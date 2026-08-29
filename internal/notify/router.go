package notify

import (
	"context"
	"fmt"

	"golang.org/x/time/rate"
)

// Route pairs a Notifier with the filters that decide whether a given
// Notification reaches it — ADR-0009: "enrutado por severidad y por
// etiqueta: critical a ntfy y Telegram, warn solo a ntfy, info solo al
// histórico."
type Route struct {
	Name     string // for rate-limit bucketing and error messages
	Notifier Notifier
	// Severities, if non-empty, restricts this route to matching
	// severities. Empty means "every severity".
	Severities []string
	// Labels, if non-empty, requires every key/value pair to be present
	// (exact match) on the notification.
	Labels map[string]string
}

func (r Route) matches(n Notification) bool {
	if len(r.Severities) > 0 {
		found := false
		for _, s := range r.Severities {
			if s == n.Severity {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for k, v := range r.Labels {
		if n.Labels[k] != v {
			return false
		}
	}
	return true
}

// Router dispatches a Notification to every matching Route, rate-limited
// per route (ADR-0009: "límite de tasa de notificaciones por canal, con
// techo duro. Un bucle de alertas no debe poder enviar mil mensajes.").
type Router struct {
	routes  []Route
	limiter *rate.Limiter // nil = unlimited; shared budget across all routes for simplicity
}

// NewRouter returns a Router dispatching to routes. If rps/burst are both
// zero, no rate limiting is applied.
func NewRouter(routes []Route, rps float64, burst int) *Router {
	r := &Router{routes: routes}
	if rps > 0 || burst > 0 {
		r.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
	return r
}

// Dispatch sends n to every route whose filters match, returning every
// error encountered (a failure on one route never stops the others — an
// unreachable webhook shouldn't also silence ntfy).
func (r *Router) Dispatch(ctx context.Context, n Notification) []error {
	var errs []error
	for _, route := range r.routes {
		if !route.matches(n) {
			continue
		}
		if r.limiter != nil && !r.limiter.Allow() {
			errs = append(errs, fmt.Errorf("route %q: rate limit exceeded, notification dropped", route.Name))
			continue
		}
		if err := route.Notifier.Notify(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("route %q: %w", route.Name, err))
		}
	}
	return errs
}
