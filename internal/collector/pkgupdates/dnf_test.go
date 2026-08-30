package pkgupdates

import (
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/spool"
)

func TestDnfItems_ReadsSpoolEntry(t *testing.T) {
	dir := t.TempDir()
	data := struct {
		Updates []dnfSpoolUpdate `json:"updates"`
	}{
		Updates: []dnfSpoolUpdate{
			{Name: "bash", Arch: "x86_64", Version: "5.1.8-6.el9", Repo: "baseos"},
			{Name: "kernel", Arch: "x86_64", Version: "5.14.0-284.el9", Repo: "baseos"},
		},
	}
	if err := spool.WriteAtomic(dir, "dnfupdates", 1, data, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := dnfItems(dir)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].ID != "dnf:bash.x86_64" {
		t.Fatalf("unexpected item ID: %q", items[0].ID)
	}
	if items[0].Attrs["candidate_version"] != "5.1.8-6.el9" {
		t.Fatalf("unexpected candidate_version: %+v", items[0].Attrs)
	}
}

func TestDnfItems_NoSpoolEntryYieldsNoItemsNotError(t *testing.T) {
	items := dnfItems(filepath.Join(t.TempDir(), "no-spool"))
	if items != nil {
		t.Fatalf("expected nil items without a dnf spool entry, got %+v", items)
	}
}

func TestDnfItems_EmptyUpdatesYieldsNoItems(t *testing.T) {
	dir := t.TempDir()
	data := struct {
		Updates []dnfSpoolUpdate `json:"updates"`
	}{}
	if err := spool.WriteAtomic(dir, "dnfupdates", 1, data, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := dnfItems(dir)
	if len(items) != 0 {
		t.Fatalf("expected no items on a fully up-to-date host, got %+v", items)
	}
}
