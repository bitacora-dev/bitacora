package logstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanIndex_ReconstructsFromDirectoryScan(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ts := time.Now()

	if _, err := s.Append(sampleLine("host-a", "journald", "one", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.Append(sampleLine("host-b", "docker", "two", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.FlushAll(); err != nil {
		t.Fatalf("unexpected error flushing: %v", err)
	}

	result, err := ScanIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error scanning: %v", err)
	}
	if len(result.Blocks) != 2 {
		t.Fatalf("expected 2 blocks reconstructed from the scan, got %d", len(result.Blocks))
	}
	if len(result.OrphanPayloads) != 0 || len(result.OrphanMeta) != 0 || len(result.Corrupt) != 0 {
		t.Fatalf("expected a clean scan, got %+v", result)
	}
	if result.TotalRawBytes == 0 || result.TotalCompBytes == 0 {
		t.Fatalf("expected nonzero totals, got raw=%d comp=%d", result.TotalRawBytes, result.TotalCompBytes)
	}
}

func TestScanIndex_DetectsOrphanPayload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Append(sampleLine("host-a", "journald", "one", time.Now())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	meta, err := s.Flush("host-a", "journald")
	if err != nil || meta == nil {
		t.Fatalf("unexpected error/nil meta: %v %+v", err, meta)
	}

	// Delete the sidecar, leaving an orphan .zst payload.
	metaPath := filepath.Join(dir, filepath.Dir(meta.Path), meta.BlockID+".meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("unexpected error removing sidecar: %v", err)
	}

	result, err := ScanIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphanPayloads) != 1 {
		t.Fatalf("expected 1 orphan payload, got %+v", result.OrphanPayloads)
	}
	if len(result.Blocks) != 0 {
		t.Fatalf("expected 0 reconstructed blocks without a sidecar, got %d", len(result.Blocks))
	}
}

func TestScanIndex_DetectsOrphanMeta(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Append(sampleLine("host-a", "journald", "one", time.Now())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	meta, err := s.Flush("host-a", "journald")
	if err != nil || meta == nil {
		t.Fatalf("unexpected error/nil meta: %v %+v", err, meta)
	}

	if err := os.Remove(filepath.Join(dir, meta.Path)); err != nil {
		t.Fatalf("unexpected error removing payload: %v", err)
	}

	result, err := ScanIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphanMeta) != 1 {
		t.Fatalf("expected 1 orphan meta, got %+v", result.OrphanMeta)
	}
}

func TestScanIndex_DetectsCorruptMeta(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Append(sampleLine("host-a", "journald", "one", time.Now())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	meta, err := s.Flush("host-a", "journald")
	if err != nil || meta == nil {
		t.Fatalf("unexpected error/nil meta: %v %+v", err, meta)
	}

	metaPath := filepath.Join(dir, filepath.Dir(meta.Path), meta.BlockID+".meta.json")
	if err := os.WriteFile(metaPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("unexpected error corrupting sidecar: %v", err)
	}

	result, err := ScanIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Corrupt) != 1 {
		t.Fatalf("expected 1 corrupt entry, got %+v", result.Corrupt)
	}
}

func TestScanIndex_EmptyDirIsClean(t *testing.T) {
	result, err := ScanIndex(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Blocks) != 0 {
		t.Fatalf("expected no blocks in an empty dir, got %d", len(result.Blocks))
	}
}

func TestScanIndex_NonexistentDirIsNotAnError(t *testing.T) {
	result, err := ScanIndex(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected a missing base dir to be treated as empty, not an error: %v", err)
	}
	if len(result.Blocks) != 0 {
		t.Fatalf("expected no blocks, got %d", len(result.Blocks))
	}
}
