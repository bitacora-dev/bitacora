package job

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSpoolAndReadSpool_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	j1 := sampleJob()
	j1.ID = "01J0000000000000000000AAAA"
	j2 := sampleJob()
	j2.ID = "01J0000000000000000000BBBB"

	if err := WriteSpool(dir, j1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteSpool(dir, j2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spooled, err := ReadSpool(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 2 {
		t.Fatalf("expected 2 spooled jobs, got %d", len(spooled))
	}
	if spooled[0].Job.ID != j1.ID || spooled[1].Job.ID != j2.ID {
		t.Fatalf("expected jobs in ID (chronological) order, got %s then %s", spooled[0].Job.ID, spooled[1].Job.ID)
	}
}

func TestReadSpool_EmptyDirReturnsNoJobsNoError(t *testing.T) {
	dir := t.TempDir()
	spooled, err := ReadSpool(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 0 {
		t.Fatalf("expected no spooled jobs, got %d", len(spooled))
	}
}

func TestReadSpool_MissingDirIsNotAnError(t *testing.T) {
	spooled, err := ReadSpool(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spooled != nil {
		t.Fatalf("expected nil, got %v", spooled)
	}
}

func TestReadSpool_SkipsCorruptEntry(t *testing.T) {
	dir := t.TempDir()

	good := sampleJob()
	good.ID = "01J0000000000000000000GOOD"
	if err := WriteSpool(dir, good); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01J0000000000000000000BAD.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spooled, err := ReadSpool(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 1 || spooled[0].Job.ID != good.ID {
		t.Fatalf("expected only the good entry, got %+v", spooled)
	}
}

func TestRemoveSpool_MissingFileIsNotAnError(t *testing.T) {
	if err := RemoveSpool(filepath.Join(t.TempDir(), "gone.json")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveSpool_DeletesTheFile(t *testing.T) {
	dir := t.TempDir()
	j := sampleJob()
	if err := WriteSpool(dir, j); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := filepath.Join(dir, j.ID+".json")

	if err := RemoveSpool(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spooled, err := ReadSpool(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spooled) != 0 {
		t.Fatalf("expected the entry to be gone, got %+v", spooled)
	}
}
