// SPDX-License-Identifier: Apache-2.0

package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAtomic_ThenReadDirRoundTrips(t *testing.T) {
	dir := t.TempDir()

	type smartData struct {
		Devices int `json:"devices"`
	}

	if err := WriteAtomic(dir, "smart", 1, smartData{Devices: 3}, nil); err != nil {
		t.Fatalf("unexpected error writing spool entry: %v", err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error reading spool dir: %v", err)
	}

	entry, ok := entries["smart"]
	if !ok {
		t.Fatal("expected a smart entry in the spool")
	}
	if entry.Schema != 1 {
		t.Fatalf("expected schema 1, got %d", entry.Schema)
	}

	var got smartData
	if err := json.Unmarshal(entry.Data, &got); err != nil {
		t.Fatalf("unexpected error unmarshaling data: %v", err)
	}
	if got.Devices != 3 {
		t.Fatalf("expected 3 devices, got %d", got.Devices)
	}
}

func TestWriteAtomic_CarriesNonFatalErrors(t *testing.T) {
	dir := t.TempDir()

	if err := WriteAtomic(dir, "smart", 1, map[string]any{}, []string{"nvme1: timeout"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries["smart"].Errors) != 1 || entries["smart"].Errors[0] != "nvme1: timeout" {
		t.Fatalf("expected the non-fatal error to round-trip, got %+v", entries["smart"].Errors)
	}
}

func TestReadDir_MissingDirReturnsEmptyNotError(t *testing.T) {
	entries, err := ReadDir("/does/not/exist/bitacora-spool-test")
	if err != nil {
		t.Fatalf("expected no error for a missing spool dir, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestReadDir_SkipsCorruptFiles(t *testing.T) {
	dir := t.TempDir()

	if err := WriteAtomic(dir, "smart", 1, map[string]any{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mdadm.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("unexpected error writing corrupt fixture: %v", err)
	}

	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := entries["smart"]; !ok {
		t.Fatal("expected the valid smart entry to still be readable")
	}
	if len(entries) != 1 {
		t.Fatalf("expected the corrupt file to be skipped, got %d entries", len(entries))
	}
}

func TestEntry_Stale(t *testing.T) {
	now := time.Now()
	fresh := Entry{TS: now.Add(-5 * time.Minute)}
	stale := Entry{TS: now.Add(-46 * time.Minute)}
	interval := 15 * time.Minute

	if fresh.Stale(now, interval) {
		t.Fatal("expected a 5-minute-old entry with a 15-minute interval not to be stale")
	}
	if !stale.Stale(now, interval) {
		t.Fatal("expected a 46-minute-old entry with a 15-minute interval to be stale (>3x)")
	}
}
