package extraction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Engine matches log lines against a set of rules and turns a match into
// an Event.
type Engine struct {
	rules     []*Rule
	enrichers Enrichers
}

// NewEngine returns an Engine evaluating rules in order (first match
// wins — a log line becomes at most one event) using enrichers to read
// /proc and /sys.
func NewEngine(rules []*Rule, enrichers Enrichers) *Engine {
	return &Engine{rules: rules, enrichers: enrichers}
}

// Process matches line against every rule and returns the resulting
// Event, or nil if nothing matched — which is the normal case, not an
// error: most log lines don't correspond to a significant event.
func (e *Engine) Process(ctx context.Context, line schema.LogLine) (*schema.Event, error) {
	for _, r := range e.rules {
		if r.Source != "" && r.Source != line.Source {
			continue
		}

		match := r.re.FindStringSubmatch(line.Message)
		if match == nil {
			continue
		}

		attrs := namedGroups(r.re, match)

		funcs := e.enrichers.funcs()
		for _, name := range r.Emit.Enrich {
			if fn, ok := funcs[name]; ok {
				fn(attrs) // best-effort; enrichers never error, see enrich.go
			}
		}

		event, err := buildEvent(r, line, attrs)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.ID, err)
		}
		return event, nil
	}
	return nil, nil
}

func namedGroups(re *regexp.Regexp, match []string) map[string]string {
	names := re.SubexpNames()
	attrs := make(map[string]string, len(names))
	for i, name := range names {
		if i == 0 || name == "" {
			continue
		}
		attrs[name] = match[i]
	}
	return attrs
}

func buildEvent(r *Rule, line schema.LogLine, attrs map[string]string) (*schema.Event, error) {
	title, err := renderTitle(r.Emit.Title, attrs)
	if err != nil {
		return nil, fmt.Errorf("rendering title: %w", err)
	}
	if title == "" {
		title = r.Emit.Type
	}

	var subject schema.EventSubject
	if comm, ok := attrs["comm"]; ok {
		subject.Kind = "process"
		subject.Name = comm
		if pid, err := strconv.Atoi(attrs["pid"]); err == nil {
			subject.PID = pid
		}
	}

	schemaAttrs := make(schema.Labels, len(attrs))
	for k, v := range attrs {
		schemaAttrs[k] = v
	}

	return &schema.Event{
		ID:          ulid.Make().String(),
		TS:          line.TS,
		HostID:      line.HostID,
		Source:      line.Source,
		Type:        r.Emit.Type,
		Severity:    schema.Severity(r.Emit.Severity),
		Title:       title,
		Subject:     subject,
		Attrs:       schemaAttrs,
		Fingerprint: fingerprint(r, attrs),
		Schema:      schema.CurrentSchemaVersion,
	}, nil
}

func renderTitle(tmplText string, attrs map[string]string) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	tmpl, err := template.New("title").Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, attrs); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// fingerprint hashes the fields that identify a *recurrence* of the same
// underlying problem (ADR-0006: "dos segfaults del mismo binario en la
// misma CPU comparten fingerprint"), not the fields that make one
// occurrence unique (pid, exact address, timestamp).
func fingerprint(r *Rule, attrs map[string]string) string {
	fields := r.Emit.FingerprintFields
	if len(fields) == 0 {
		fields = make([]string, 0, len(attrs))
		for k := range attrs {
			fields = append(fields, k)
		}
		sort.Strings(fields)
	}

	h := sha256.New()
	h.Write([]byte(r.Emit.Type))
	for _, f := range fields {
		h.Write([]byte{0x1f})
		h.Write([]byte(f))
		h.Write([]byte{'='})
		h.Write([]byte(attrs[f]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
