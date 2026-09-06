// Package hubpipeline wires persisted log lines to extraction, alerting and
// notifications. It is deliberately hub-only: agents only send canonical data
// and never receive alert rules (ADR-0009).
package hubpipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bitacora-dev/bitacora/internal/alerting"
	"github.com/bitacora-dev/bitacora/internal/extraction"
	"github.com/bitacora-dev/bitacora/internal/notify"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Default directories keep operator-owned configuration outside package data.
// Missing directories/files simply disable their optional configuration.
const (
	DefaultExtractionRulesDir = "/etc/bitacora/rules"
	DefaultAlertRulesDir      = "/etc/bitacora/alert-rules"
	DefaultNotificationsPath  = "/etc/bitacora/notifications.yaml"
)

// EventInserter is the narrow persistent event boundary needed after a log
// line has been promoted by extraction.
type EventInserter interface {
	InsertEvent(context.Context, schema.Event) error
}

// Config supplies hub-side, operator-owned pipeline configuration.
type Config struct {
	ExtractionRulesDir string
	AlertRulesDir      string
	NotificationsPath  string
	HubURL             string
}

// Processor runs only after the raw log line was durably appended. It first
// stores any promoted Event, then evaluates matching event rules and sends
// notifications for fresh alert transitions.
type Processor struct {
	mu        sync.Mutex // guards event-rule occurrence windows and alert transitions
	events    EventInserter
	extractor *extraction.Engine
	alerts    *alerting.Manager
	rules     []*eventRule
	router    *notify.Router
	hubURL    string
	now       func() time.Time
}

// New constructs a production pipeline using embedded default extraction
// rules plus optional operator configuration.
func New(events EventInserter, cfg Config) (*Processor, error) {
	if events == nil {
		return nil, fmt.Errorf("hub pipeline: event store is required")
	}

	rules, err := extraction.LoadDefaults()
	if err != nil {
		return nil, err
	}
	if cfg.ExtractionRulesDir != "" {
		custom, err := loadExtractionRules(cfg.ExtractionRulesDir)
		if err != nil {
			return nil, err
		}
		rules = append(rules, custom...)
	}

	alertRules, err := loadEventRules(cfg.AlertRulesDir)
	if err != nil {
		return nil, err
	}
	router, err := loadRouter(cfg.NotificationsPath)
	if err != nil {
		return nil, err
	}

	return &Processor{
		events:    events,
		extractor: extraction.NewEngine(rules, extraction.DefaultEnrichers()),
		alerts:    alerting.NewManager(nil),
		rules:     alertRules,
		router:    router,
		hubURL:    cfg.HubURL,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

// Process handles one already-persisted line. A bad extraction rule or a
// transient notification failure is logged and does not make the ingest batch
// fail: transport has already accepted the batch and raw logs remain durable.
func (p *Processor) Process(ctx context.Context, line schema.LogLine) error {
	event, err := p.extractor.Process(ctx, line)
	if err != nil {
		return err
	}
	if event == nil {
		return nil
	}
	event.TSReceived = p.now()
	if err := p.events.InsertEvent(ctx, *event); err != nil {
		return fmt.Errorf("storing extracted event: %w", err)
	}

	p.mu.Lock()
	var notifications []notify.Notification
	for _, rule := range p.rules {
		if rule.OnEvent != event.Type {
			continue
		}
		labels := rule.labelsFor(*event)
		count := rule.observe(*event, labels)
		alert, shouldNotify := p.alerts.Evaluate(event.TS, rule.ID, labels, rule.Severity, count >= rule.Threshold.Count, float64(count), 0)
		if !shouldNotify {
			continue
		}
		notifications = append(notifications, notify.Notification{
			RuleID: rule.ID, Labels: labels, Severity: rule.Severity,
			State: string(alert.State), Value: alert.Value, At: event.TS,
			DeepLink: notify.DeepLink(p.hubURL, labels, event.TS),
		})
	}
	p.mu.Unlock()

	for _, notification := range notifications {
		for _, err := range p.router.Dispatch(ctx, notification) {
			slog.Error("hub pipeline: dispatching alert notification", "rule_id", notification.RuleID, "err", err)
		}
	}
	return nil
}

func loadExtractionRules(dir string) ([]*extraction.Rule, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat extraction rules dir: %w", err)
	}
	rules, err := extraction.LoadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("loading extraction rules: %w", err)
	}
	return rules, nil
}

type eventRule struct {
	ID        string   `yaml:"id"`
	OnEvent   string   `yaml:"on_event"`
	GroupBy   []string `yaml:"group_by"`
	Threshold struct {
		Count  int      `yaml:"count"`
		Window duration `yaml:"window"`
	} `yaml:"threshold"`
	Severity string `yaml:"severity"`

	occurrences map[string][]time.Time
}

// duration accepts the human-facing Go duration syntax used by ADR examples,
// such as "24h". yaml.v3 does not parse time.Duration strings on its own.
type duration time.Duration

func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = duration(parsed)
	return nil
}

func (r *eventRule) validate() error {
	if r.ID == "" || r.OnEvent == "" || r.Severity == "" {
		return fmt.Errorf("event rule requires id, on_event and severity")
	}
	if r.Threshold.Count < 1 || r.Threshold.Window <= 0 {
		return fmt.Errorf("event rule %q requires threshold.count >= 1 and a positive threshold.window", r.ID)
	}
	r.occurrences = make(map[string][]time.Time)
	return nil
}

func loadEventRules(dir string) ([]*eventRule, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading alert rules dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	rules := make([]*eventRule, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(dir + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("reading alert rule %s: %w", name, err)
		}
		var rule eventRule
		if err := yaml.Unmarshal(data, &rule); err != nil {
			return nil, fmt.Errorf("parsing alert rule %s: %w", name, err)
		}
		if err := rule.validate(); err != nil {
			return nil, fmt.Errorf("alert rule %s: %w", name, err)
		}
		rules = append(rules, &rule)
	}
	return rules, nil
}

func (r *eventRule) labelsFor(event schema.Event) map[string]string {
	labels := map[string]string{"host_id": event.HostID}
	for _, field := range r.GroupBy {
		if value := eventField(event, field); value != "" {
			labels[field] = value
		}
	}
	return labels
}

func eventField(event schema.Event, field string) string {
	switch field {
	case "host_id":
		return event.HostID
	case "subject.name":
		return event.Subject.Name
	default:
		if key, ok := strings.CutPrefix(field, "attrs."); ok {
			return event.Attrs[key]
		}
		return ""
	}
}

func (r *eventRule) observe(event schema.Event, labels map[string]string) int {
	key := alerting.Fingerprint(r.ID, labels)
	cutoff := event.TS.Add(-time.Duration(r.Threshold.Window))
	occurrences := r.occurrences[key]
	kept := occurrences[:0]
	for _, at := range occurrences {
		if !at.Before(cutoff) {
			kept = append(kept, at)
		}
	}
	kept = append(kept, event.TS)
	r.occurrences[key] = kept
	return len(kept)
}
