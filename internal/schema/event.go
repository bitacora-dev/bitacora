// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"fmt"
	"strings"
	"time"
)

// Event is a discrete, significant fact — the piece that makes timeline
// correlation possible (ADR-0006).
type Event struct {
	ID          string       `json:"id"`
	TS          time.Time    `json:"ts"`
	TSReceived  time.Time    `json:"ts_received,omitempty"`
	HostID      string       `json:"host_id"`
	Source      string       `json:"source"`
	Type        string       `json:"type"`
	Severity    Severity     `json:"severity"`
	Title       string       `json:"title"`
	Subject     EventSubject `json:"subject,omitempty"`
	Attrs       Labels       `json:"attrs,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	LogRefs     []LogRef     `json:"log_refs,omitempty"`
	Schema      int          `json:"schema"`
}

// Validate enforces the ADR-0006 required fields and conventions.
func (e Event) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event: id is required")
	}
	if e.HostID == "" {
		return fmt.Errorf("event %q: host_id is required", e.ID)
	}
	if e.TS.IsZero() {
		return fmt.Errorf("event %q: ts is required", e.ID)
	}
	if e.Source == "" {
		return fmt.Errorf("event %q: source is required", e.ID)
	}
	if e.Type == "" || !strings.Contains(e.Type, ".") {
		return fmt.Errorf("event %q: type must be non-empty and namespaced (e.g. %q), got %q", e.ID, "kernel.segfault", e.Type)
	}
	if !e.Severity.valid() {
		return fmt.Errorf("event %q: invalid severity %q", e.ID, e.Severity)
	}
	if e.Title == "" {
		return fmt.Errorf("event %q: title is required", e.ID)
	}
	if e.Schema < 1 {
		return fmt.Errorf("event %q: schema must be >= 1", e.ID)
	}
	return nil
}
