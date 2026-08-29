package main

import (
	"os"
	"testing"
)

func TestDetectTrigger_Systemd(t *testing.T) {
	t.Setenv("INVOCATION_ID", "abc123")
	if got := detectTrigger(); got != "systemd" {
		t.Errorf("detectTrigger() = %q, want %q", got, "systemd")
	}
}

func TestDetectTrigger_JournalStream(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "8:1234")
	if got := detectTrigger(); got != "systemd" {
		t.Errorf("detectTrigger() = %q, want %q", got, "systemd")
	}
}

func TestIsCronParent_NoSuchProcess(t *testing.T) {
	if isCronParent(999999999) {
		t.Error("expected false for a nonexistent pid")
	}
}

func TestDetectTrigger_DefaultsToManual(t *testing.T) {
	os.Unsetenv("INVOCATION_ID")
	os.Unsetenv("JOURNAL_STREAM")
	// Whatever actually launched `go test` isn't cron, so this should be
	// "manual" in any real test environment.
	if got := detectTrigger(); got != "manual" {
		t.Errorf("detectTrigger() = %q, want %q", got, "manual")
	}
}
