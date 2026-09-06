# Hub extraction, alerting, and notification configuration

The hub persists a raw `LogLine` before it attempts extraction. A matching
extraction rule creates a canonical Event; event alert rules then evaluate that
persisted Event and a fresh `firing` or `resolved` transition is routed to the
configured destinations. This is hub-only behavior: agents never receive rule
or notification configuration.

Built-in extraction rules are embedded in `bitacora-hub`. Add local extraction
rules as YAML files under `/etc/bitacora/rules/`; upgrades never modify that
directory. Alert rules live in `/etc/bitacora/alert-rules/`. A missing optional
directory means no operator rules, not a startup failure.

```yaml
# /etc/bitacora/rules/service-failed.yaml
id: service-failed
source: journald
match: 'service (?P<unit>\S+) entered failed state'
emit:
  type: service.entered_failed
  severity: error
  title: "service {{.unit}} entered failed state"
  fingerprint_fields: [unit]
```

```yaml
# /etc/bitacora/alert-rules/service-failed.yaml
id: service-failed-recurrent
on_event: service.entered_failed
group_by: [attrs.unit]
threshold:
  count: 3
  window: 24h
severity: error
```

The current first slice deliberately supports event-count rules only. The
promoted Event is persistent, but alert state, occurrence windows, and
transition history are in memory and reset when the hub restarts. An event rule
is re-evaluated only by a later matching Event; it has no scheduled expiry.
Metric thresholds, deadman rules, persisted alert lifecycle/history, grouping,
retries, per-destination rate limits, and the external hub deadman remain
follow-up work; they must not be inferred from an unimplemented configuration
key. The `rate_limit` block applies the existing router's one shared budget
across configured routes.

Configure notification routes in `/etc/bitacora/notifications.yaml`. The
system-log route is always active and cannot be removed. Give this file mode
`0600` when it contains Telegram or SMTP credentials.

```yaml
rate_limit:
  rps: 0.2
  burst: 3
routes:
  - name: mobile
    severities: [error, critical]
    ntfy:
      topic_url: https://ntfy.example.invalid/bitacora
  - name: automation
    severities: [critical]
    webhook:
      url: https://automation.example.invalid/bitacora-alerts
  - name: fallback-email
    severities: [critical]
    smtp:
      host: smtp.example.invalid
      port: 587
      username: alerts@example.invalid
      password: replace-me
      from: alerts@example.invalid
      to: [operator@example.invalid]
```

Each route has exactly one destination: `ntfy`, `webhook`, `telegram`, or
`smtp`. `severities` and `labels` are optional filters. `bitacora-hub` accepts
`-extraction-rules-dir`, `-alert-rules-dir`, `-notifications`, and `-hub-url`
to override these paths and to put a timeline deep link in notifications.
