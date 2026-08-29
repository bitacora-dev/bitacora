package pstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func writeEntry(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestList_ReturnsOnlyDmesgEntriesSorted(t *testing.T) {
	root := t.TempDir()
	writeEntry(t, root, "dmesg-efi-200", "second oops")
	writeEntry(t, root, "dmesg-efi-100", "first oops")
	writeEntry(t, root, "console-efi-150", "console output, not a crash dump")

	entries, err := List(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 dmesg entries (console- excluded), got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "dmesg-efi-100" || entries[1].Name != "dmesg-efi-200" {
		t.Fatalf("expected sorted order, got %s then %s", entries[0].Name, entries[1].Name)
	}
}

func TestList_MissingRootIsNotAnError(t *testing.T) {
	entries, err := List(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil, got %v", entries)
	}
}

func TestToEvent_BuildsAKernelCrashDumpEvent(t *testing.T) {
	e := Entry{Name: "dmesg-efi-100", Content: []byte("Oops: 0000 [#1] SMP\nRIP: ...\n")}
	now := time.Date(2026, 8, 29, 3, 0, 0, 0, time.UTC)

	event := ToEvent("host-a", e, now)

	if event.Type != "kernel.crash_dump" {
		t.Errorf("Type = %q", event.Type)
	}
	if event.Severity != schema.SeverityCritical {
		t.Errorf("Severity = %q", event.Severity)
	}
	if event.HostID != "host-a" {
		t.Errorf("HostID = %q", event.HostID)
	}
	if !strings.Contains(event.Attrs["excerpt"], "Oops: 0000") {
		t.Errorf("expected the excerpt to contain the dump content, got %q", event.Attrs["excerpt"])
	}
	if event.Attrs["pstore_file"] != "dmesg-efi-100" {
		t.Errorf("pstore_file = %q", event.Attrs["pstore_file"])
	}
	if err := event.Validate(); err != nil {
		t.Errorf("expected a valid Event, got: %v", err)
	}
}

func TestToEvent_TruncatesAnOversizedExcerpt(t *testing.T) {
	huge := strings.Repeat("x", MaxExcerptBytes+500)
	e := Entry{Name: "dmesg-efi-1", Content: []byte(huge)}

	event := ToEvent("host-a", e, time.Now())

	if len(event.Attrs["excerpt"]) != MaxExcerptBytes {
		t.Fatalf("expected excerpt truncated to %d bytes, got %d", MaxExcerptBytes, len(event.Attrs["excerpt"]))
	}
	if event.Attrs["excerpt_truncated"] != "true" {
		t.Fatal("expected excerpt_truncated=true")
	}
	if event.Attrs["size_bytes"] != "4500" {
		t.Fatalf("expected size_bytes to report the full original size, got %q", event.Attrs["size_bytes"])
	}
}

func TestConsume_ConvertsAndRemovesEntries(t *testing.T) {
	root := t.TempDir()
	writeEntry(t, root, "dmesg-efi-100", "oops content")

	events, errs := Consume(root, "host-a", time.Now())
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if _, err := os.Stat(filepath.Join(root, "dmesg-efi-100")); !os.IsNotExist(err) {
		t.Fatalf("expected the pstore entry to have been removed after consuming it, stat err: %v", err)
	}
}

func TestConsume_EmptyRootReturnsNothing(t *testing.T) {
	events, errs := Consume(t.TempDir(), "host-a", time.Now())
	if len(events) != 0 || len(errs) != 0 {
		t.Fatalf("expected nothing, got events=%v errs=%v", events, errs)
	}
}
