// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"testing"
	"time"
)

func validEvent() Event {
	return Event{
		ID:       "01J8XQZZZZZZZZZZZZZZZZZZZZ",
		TS:       time.Now(),
		HostID:   "01J8XQZZZZZZZZZZZZZZZZZZZZ",
		Source:   "kernel",
		Type:     "kernel.segfault",
		Severity: SeverityError,
		Title:    "segfault in node (cpu 8)",
		Schema:   CurrentSchemaVersion,
	}
}

func TestEvent_ValidateAcceptsWellFormedEvent(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("expected valid event to pass, got %v", err)
	}
}

func TestEvent_ValidateRejectsInvalidSeverity(t *testing.T) {
	e := validEvent()
	e.Severity = "catastrophic"
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func TestEvent_ValidateRejectsNonNamespacedType(t *testing.T) {
	e := validEvent()
	e.Type = "segfault"
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for a type without a namespace prefix")
	}
}

func TestEvent_ValidateRejectsMissingHostID(t *testing.T) {
	e := validEvent()
	e.HostID = ""
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for missing host_id")
	}
}

func TestEvent_ValidateRejectsZeroSchema(t *testing.T) {
	e := validEvent()
	e.Schema = 0
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for schema < 1")
	}
}
