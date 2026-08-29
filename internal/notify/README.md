# internal/notify

The alert engine's notifiers (ADR-0009): ntfy (default), generic webhook,
Telegram, SMTP, and the always-on system log.

- `notify.go` — `Notification` (what gets sent) and `Notifier`
  (`Notify(ctx, Notification) error`). `DeepLink` builds a URL into the
  hub's single-page timeline centered on the alert's instant — ADR-0009:
  "toda alerta notificada incluye enlace profundo a la línea temporal."
- `ntfy.go` — the default notifier. Severity maps to ntfy's priority
  levels (`critical`→urgent … `debug`→min); the deep link goes in the
  `Click` header so tapping the push notification opens it directly.
- `webhook.go` — generic JSON POST. ADR-0009 mentions this as the future
  Task Queue AI integration point; the richer payload (timeline context,
  deep report link) is a phase-4 item, not built here.
- `telegram.go` — a bot's `sendMessage` call. `apiBaseURL` is unexported
  and only exists so tests can point at a fake server instead of
  `api.telegram.org`.
- `smtp.go` — `NewSMTPNotifier` wires the real `net/smtp.SendMail`; the
  unexported `sendMail` field is injected in tests instead, since spinning
  up a real SMTP server for a unit test isn't worth it.
- `log.go` — always active, not configurable (ADR-0009).
- `router.go` — `Router.Dispatch` sends to every `Route` whose
  `Severities`/`Labels` filters match, rate-limited (`golang.org/x/time/rate`,
  shared budget across routes — "un bucle de alertas no debe poder enviar
  mil mensajes"). One route failing never stops the others.

## What's NOT here

- Wiring `alerting.Manager`'s `shouldNotify` output into a `Router` —
  that's the hub's own alert-evaluation loop, which doesn't exist yet
  (same followup as the collectors/agent run loop).
- The full Task Queue AI webhook payload (±15min timeline context, deep
  diagnostic link) — ADR-0009 defers this to phase 4 itself.
- Alert **grouping** (same-host alerts in a short window notified
  together) — ADR-0009 mentions it, out of this task's scope.
