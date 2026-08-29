package agentbuffer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func logLineItem(hostID, message string, ts time.Time) Item {
	return Item{
		Priority: PriorityLogLine,
		TS:       ts,
		LogLine: &schema.LogLine{
			TS: ts, HostID: hostID, Source: "journald", Message: message,
		},
	}
}

func metricItem(hostID string, value float64, ts time.Time) Item {
	return Item{
		Priority: PriorityMetric,
		TS:       ts,
		Metric: &schema.Metric{
			Name: "bitacora_cpu_usage_ratio", HostID: hostID, Value: value, Timestamp: ts,
		},
	}
}

func eventItem(hostID, title string, ts time.Time) Item {
	return Item{
		Priority: PriorityEvent,
		TS:       ts,
		Event: &schema.Event{
			ID: "evt-" + title, TS: ts, HostID: hostID, Source: "kernel",
			Type: "kernel.segfault", Severity: schema.SeverityError, Title: title,
			Schema: schema.CurrentSchemaVersion,
		},
	}
}

func TestBuffer_AppendAndReadBack(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	if _, err := b.Append(logLineItem("host-a", "hello", ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := b.Append(metricItem("host-a", 0.5, ts)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := b.Len(); got != 2 {
		t.Fatalf("expected 2 buffered items, got %d", got)
	}

	items, err := b.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || items[0].LogLine == nil || items[1].Metric == nil {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestBuffer_SequenceNumbersAreMonotonic(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	seq1, err := b.Append(logLineItem("host-a", "a", ts))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seq2, err := b.Append(logLineItem("host-a", "b", ts))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("expected monotonically increasing sequence numbers, got %d then %d", seq1, seq2)
	}
}

func TestBuffer_RotatesSegmentsAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	// A tiny segment size so a handful of items forces multiple rotations.
	b, err := Open(dir, WithSegmentBytes(200))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	for i := 0; i < 20; i++ {
		if _, err := b.Append(logLineItem("host-a", "a reasonably long log line to fill segments", ts)); err != nil {
			t.Fatalf("unexpected error at item %d: %v", i, err)
		}
	}

	if len(b.sealed) == 0 {
		t.Fatal("expected at least one segment to have been sealed by rotation")
	}

	items, err := b.oldestItems(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 20 {
		t.Fatalf("expected all 20 items readable back across segments, got %d", len(items))
	}
	for i, it := range items {
		if int(it.Seq) != i+1 {
			t.Fatalf("expected items in seq order across segments, got seq %d at index %d", it.Seq, i)
		}
	}
}

func TestBuffer_SurvivesUncleanRestart(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ts := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Simulate a crash: no Close(), no seal — just walk away from the
	// active .wal file exactly as an unclean shutdown would leave it.
	// (Not calling b.Close() is the point of this test.)

	b2, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error reopening after unclean shutdown: %v", err)
	}
	defer b2.Close()

	if got := b2.Len(); got != 5 {
		t.Fatalf("expected all 5 items to survive an unclean restart, got %d", got)
	}

	// Continuing to append after recovery must keep sequence numbers
	// strictly increasing, not restart from 1 and collide.
	seq, err := b2.Append(logLineItem("host-a", "after restart", ts))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seq <= 5 {
		t.Fatalf("expected the sequence counter to resume above 5 after recovery, got %d", seq)
	}
}

func TestBuffer_SurvivesCleanRestart(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ts := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := b.Append(logLineItem("host-a", "line", ts)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}

	b2, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b2.Close()

	if got := b2.Len(); got != 3 {
		t.Fatalf("expected all 3 items to survive a clean restart, got %d", got)
	}
}

func TestBuffer_OpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "outbound")
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected the buffer directory to be created: %v", err)
	}
}

func TestBuffer_AckRemovesConfirmedItems(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer b.Close()

	ts := time.Now()
	var lastSeq uint64
	for i := 0; i < 5; i++ {
		seq, err := b.Append(logLineItem("host-a", "line", ts))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if i == 2 {
			lastSeq = seq
		}
	}

	if err := b.Ack(lastSeq); err != nil {
		t.Fatalf("unexpected error acking: %v", err)
	}
	if got := b.Len(); got != 2 {
		t.Fatalf("expected 2 items remaining after acking the first 3, got %d", got)
	}

	items, err := b.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, it := range items {
		if it.Seq <= lastSeq {
			t.Fatalf("expected no acked item to remain, found seq %d", it.Seq)
		}
	}
}
