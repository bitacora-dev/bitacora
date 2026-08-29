// Package extraction implements ADR-0006's extraction rules: declarative
// YAML rules that promote a matched log line into an Event. Logs are the
// raw material; events are what's been understood from them — this
// package is the "understood from them" part.
package extraction

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EmitSpec is the "emit" block of a rule: what Event to build from a
// match.
type EmitSpec struct {
	Type string `yaml:"type"`
	// Severity must be one of schema's valid severities.
	Severity string `yaml:"severity"`
	// Title is a text/template string rendered against the match's named
	// capture groups (plus anything enrichment added), e.g.
	// "segfault in {{.comm}} (cpu {{.cpu}})" — matching ADR-0006's own
	// worked example. Not part of the ADR's literal YAML shape, added
	// here because a rule needs to produce a specific, readable title —
	// the ADR's title text differs per rule and isn't derivable from
	// Type alone.
	Title string `yaml:"title"`
	// Enrich names zero or more built-in enrichers to run against the
	// match's attrs before the Event is built (ADR-0006:
	// "enrich: [cpu_from_context, core_from_cpu]"). Unknown names are a
	// load-time error — a typo here should never fail silently.
	Enrich []string `yaml:"enrich"`
	// FingerprintFields lists which attrs (by capture group name)
	// identify a *recurrence* of the same underlying problem, for
	// ADR-0006's fingerprint: "dos segfaults del mismo binario en la
	// misma CPU comparten fingerprint". Defaults to every capture group
	// if empty.
	FingerprintFields []string `yaml:"fingerprint_fields"`
}

// Rule is one extraction rule (ADR-0006).
type Rule struct {
	ID     string   `yaml:"id"`
	Source string   `yaml:"source"`
	Match  string   `yaml:"match"`
	Emit   EmitSpec `yaml:"emit"`

	re *regexp.Regexp
}

// Parse decodes and validates one rule from YAML, compiling its regex.
func Parse(data []byte) (*Rule, error) {
	var r Rule
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing rule YAML: %w", err)
	}
	if err := r.validate(); err != nil {
		return nil, err
	}

	re, err := regexp.Compile(r.Match)
	if err != nil {
		return nil, fmt.Errorf("rule %q: compiling match regex: %w", r.ID, err)
	}
	r.re = re

	return &r, nil
}

func (r *Rule) validate() error {
	if r.ID == "" {
		return fmt.Errorf("rule: id is required")
	}
	if r.Match == "" {
		return fmt.Errorf("rule %q: match is required", r.ID)
	}
	if r.Emit.Type == "" {
		return fmt.Errorf("rule %q: emit.type is required", r.ID)
	}
	if r.Emit.Severity == "" {
		return fmt.Errorf("rule %q: emit.severity is required", r.ID)
	}
	for _, name := range r.Emit.Enrich {
		if _, ok := builtinEnrichers[name]; !ok {
			return fmt.Errorf("rule %q: unknown enricher %q", r.ID, name)
		}
	}
	return nil
}

// LoadDir parses every *.yaml/*.yml file in dir as a Rule, in filename
// order (deterministic — match order matters, since Process uses
// first-match-wins).
func LoadDir(dir string) ([]*Rule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading rules dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	rules := make([]*Rule, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading rule file %s: %w", name, err)
		}
		rule, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}
