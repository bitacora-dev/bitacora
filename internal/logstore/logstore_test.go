package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func sampleLine(hostID, source, message string, ts time.Time) schema.LogLine {
	return schema.LogLine{
		TS:              ts,
		HostID:          hostID,
		Source:          source,
		UnitOrContainer: "bitacora-agent.service",
		Level:           "info",
		Message:         message,
	}
}

func TestStore_AppendBelowThresholdBuffersWithoutFlushing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, WithLimits(DefaultMaxBytes, DefaultMaxAge))

	meta, err := s.Append(sampleLine("host-a", "journald", "hello", time.Now()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected no flush yet, got %+v", meta)
	}
}

func TestStore_AppendFlushesOnSizeThreshold(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, WithLimits(10, DefaultMaxAge)) // 10 bytes: one short line trips it

	meta, err := s.Append(sampleLine("host-a", "journald", "a message over ten bytes", time.Now()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta == nil {
		t.Fatal("expected a flush once the size threshold is crossed")
	}
	if meta.NLines != 1 || meta.HostID != "host-a" || meta.Source != "journald" {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	if _, err := os.Stat(filepath.Join(dir, meta.Path)); err != nil {
		t.Fatalf("expected the block file to exist at %s: %v", meta.Path, err)
	}
}

func TestStore_AppendFlushesOnAgeThreshold(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	s := NewStore(dir, WithLimits(DefaultMaxBytes, time.Minute), WithClock(clock))

	if _, err := s.Append(sampleLine("host-a", "journald", "first", now)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now = now.Add(2 * time.Minute) // past the 1-minute age threshold
	meta, err := s.Append(sampleLine("host-a", "journald", "second", now))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta == nil {
		t.Fatal("expected a flush once the age threshold is crossed")
	}
	if meta.NLines != 2 {
		t.Fatalf("expected both lines in the flushed block, got %d", meta.NLines)
	}
}

func TestStore_FlushRoundTripsThroughCompression(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)
	if _, err := s.Append(sampleLine("host-a", "journald", "line one", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.Append(sampleLine("host-a", "journald", "line two", ts.Add(time.Second))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta, err := s.Flush("host-a", "journald")
	if err != nil {
		t.Fatalf("unexpected error flushing: %v", err)
	}
	if meta == nil {
		t.Fatal("expected a block from Flush")
	}
	if meta.NLines != 2 {
		t.Fatalf("expected 2 lines, got %d", meta.NLines)
	}
	if !meta.TSMin.Equal(ts) {
		t.Fatalf("expected ts_min %v, got %v", ts, meta.TSMin)
	}
	if meta.LevelsBitmap&levelBit["info"] == 0 {
		t.Fatalf("expected the info bit set in the levels bitmap, got %b", meta.LevelsBitmap)
	}

	raw, err := DecompressBlock(filepath.Join(dir, meta.Path))
	if err != nil {
		t.Fatalf("unexpected error decompressing block: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Fatalf("decompressed block doesn't contain both lines: %q", got)
	}
}

func TestStore_FlushWithNothingBufferedIsNoOp(t *testing.T) {
	s := NewStore(t.TempDir())
	meta, err := s.Flush("host-a", "journald")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil for an empty buffer, got %+v", meta)
	}
}

func TestStore_FlushAllFlushesEverySource(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ts := time.Now()

	if _, err := s.Append(sampleLine("host-a", "journald", "a", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.Append(sampleLine("host-a", "docker", "b", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metas, err := s.FlushAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 blocks (journald + docker), got %d", len(metas))
	}
}

func TestStore_AppendRejectsInvalidLine(t *testing.T) {
	s := NewStore(t.TempDir())
	invalid := schema.LogLine{Message: "no host_id, no source, no ts"}
	if _, err := s.Append(invalid); err == nil {
		t.Fatal("expected an invalid log line to be rejected")
	}
}
