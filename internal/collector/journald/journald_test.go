package journald

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

type fakeReader struct {
	entries []Entry
	idx     int
	closed  bool
}

func (f *fakeReader) Next(ctx context.Context) (Entry, bool, error) {
	if f.idx >= len(f.entries) {
		return Entry{}, false, nil
	}
	e := f.entries[f.idx]
	f.idx++
	return e, true, nil
}

func (f *fakeReader) Close() error {
	f.closed = true
	return nil
}

func fakeOpen(entries []Entry) OpenFunc {
	return func(cursor string) (Reader, error) {
		return &fakeReader{entries: entries}, nil
	}
}

type recordingSink struct {
	lines [][]collector.LogLine
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(source string, lines []collector.LogLine) {
	s.lines = append(s.lines, lines)
}

func sampleEntry(message, priority, cursor string) Entry {
	return Entry{
		Fields: map[string]string{
			"MESSAGE":       message,
			"PRIORITY":      priority,
			"_SYSTEMD_UNIT": "bitacora-agent.service",
			"_PID":          "4242",
		},
		RealtimeUsec: 1735689600000000, // 2025-01-01T00:00:00Z, arbitrary fixed instant
		Cursor:       cursor,
	}
}

func TestCollector_EmitsLogLinesFromEntries(t *testing.T) {
	c := New()
	entries := []Entry{
		sampleEntry("first line", "6", "cursor-1"),
		sampleEntry("second line", "3", "cursor-2"),
	}
	c.open = fakeOpen(entries)

	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.lines) != 1 || len(sink.lines[0]) != 2 {
		t.Fatalf("expected one LogLines call with 2 lines, got %+v", sink.lines)
	}

	first := sink.lines[0][0]
	if first.Message != "first line" || first.HostID != "host-a" || first.Level != "info" || first.PID != 4242 || first.UnitOrContainer != "bitacora-agent.service" {
		t.Fatalf("unexpected first line conversion: %+v", first)
	}
	second := sink.lines[0][1]
	if second.Level != "error" {
		t.Fatalf("expected priority 3 to map to 'error', got %q", second.Level)
	}
}

func TestCollector_PersistsCursorAfterCollect(t *testing.T) {
	cursorPath := filepath.Join(t.TempDir(), "journald.cursor")
	c := New()
	c.open = fakeOpen([]Entry{sampleEntry("line", "6", "cursor-final")})

	if err := c.Init(context.Background(), collector.Config{"cursor_path": cursorPath}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Collect(context.Background(), &recordingSink{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("expected the cursor file to be written: %v", err)
	}
	if string(got) != "cursor-final" {
		t.Fatalf("expected persisted cursor 'cursor-final', got %q", got)
	}
}

func TestCollector_ReopensFromPersistedCursor(t *testing.T) {
	cursorPath := filepath.Join(t.TempDir(), "journald.cursor")
	if err := os.WriteFile(cursorPath, []byte("cursor-from-last-run"), 0o600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotCursor string
	c := New()
	c.open = func(cursor string) (Reader, error) {
		gotCursor = cursor
		return &fakeReader{}, nil
	}

	if err := c.Init(context.Background(), collector.Config{"cursor_path": cursorPath}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotCursor != "cursor-from-last-run" {
		t.Fatalf("expected the reader to be opened with the persisted cursor, got %q", gotCursor)
	}
}

func TestCollector_NoNewEntriesEmitsNothingAndSkipsCursorWrite(t *testing.T) {
	cursorPath := filepath.Join(t.TempDir(), "journald.cursor")
	c := New()
	c.open = fakeOpen(nil)

	if err := c.Init(context.Background(), collector.Config{"cursor_path": cursorPath}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.lines) != 0 {
		t.Fatalf("expected no LogLines call when there's nothing new, got %+v", sink.lines)
	}
	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Fatalf("expected no cursor file to be written when nothing was read")
	}
}

func TestCollector_BoundedByMaxEntriesPerCollect(t *testing.T) {
	entries := make([]Entry, maxEntriesPerCollect+50)
	for i := range entries {
		entries[i] = sampleEntry("line", "6", "cursor")
	}

	c := New()
	c.open = fakeOpen(entries)
	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.lines[0]) != maxEntriesPerCollect {
		t.Fatalf("expected exactly %d lines in one Collect call, got %d", maxEntriesPerCollect, len(sink.lines[0]))
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	c.open = fakeOpen([]Entry{sampleEntry("line", "6", "cursor")})
	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected Collect to return an error for an already-cancelled context")
	}
}

func TestCollector_CloseClosesReader(t *testing.T) {
	c := New()
	reader := &fakeReader{}
	c.open = func(cursor string) (Reader, error) { return reader, nil }

	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reader.closed {
		t.Fatal("expected Close to close the underlying reader")
	}
}
