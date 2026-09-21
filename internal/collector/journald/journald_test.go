package journald

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	lines  [][]collector.LogLine
	events []collector.Event
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(event collector.Event)               { s.events = append(s.events, event) }
func (s *recordingSink) LogLines(source string, lines []collector.LogLine) {
	s.lines = append(s.lines, lines)
}

func (s *recordingSink) Inventory(collector.Inventory) {}

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
	if len(sink.events) != 0 {
		t.Fatalf("expected no events without segfaults, got %+v", sink.events)
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

// TestCollector_MapsTransportToSource ensures a kernel-transport journal
// entry (e.g. a segfault ring-buffer message) gets LogLine.Source ==
// "kernel", not the collector's own name — extraction.Rule.Source
// filtering (and ADR-0006's own kernel-segfault rule) depends on this to
// ever match a real kernel message.
func TestCollector_MapsTransportToSource(t *testing.T) {
	c := New()
	kernelEntry := sampleEntry("node[1234]: segfault at 0 ip 0 sp 0 error 4", "3", "cursor-1")
	kernelEntry.Fields["_TRANSPORT"] = "kernel"
	c.open = fakeOpen([]Entry{kernelEntry})

	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sink.lines[0][0].Source; got != "kernel" {
		t.Fatalf("expected Source %q, got %q", "kernel", got)
	}
}

func TestCollector_MissingTransportFallsBackToJournald(t *testing.T) {
	c := New()
	c.open = fakeOpen([]Entry{sampleEntry("line", "6", "cursor-1")}) // no _TRANSPORT set

	if err := c.Init(context.Background(), collector.Config{"cursor_path": filepath.Join(t.TempDir(), "cursor")}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sink.lines[0][0].Source; got != "journald" {
		t.Fatalf("expected fallback Source %q, got %q", "journald", got)
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

func TestParseSegfaultMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
		pid     int
		ok      bool
	}{
		{
			name:    "kernel message with library suffix",
			message: "node[1234]: segfault at 0 ip 00007f sp 00007ff error 4 in libc.so.6[7f]",
			want:    "node",
			pid:     1234,
			ok:      true,
		},
		{
			name:    "prefixed kernel timestamp without library suffix",
			message: "[ 123.456789] python3.12[42]: segfault at 0 ip 1 sp 2 error 6",
			want:    "python3.12",
			pid:     42,
			ok:      true,
		},
		{
			name:    "untrusted process name is ignored",
			message: "bad name[42]: segfault at 0 ip 1 sp 2 error 6",
			ok:      false,
		},
		{
			name:    "unexpected format is ignored",
			message: "node[42]: general protection fault",
			ok:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotComm, gotPID, gotOK := parseSegfaultMessage(tt.message)
			if gotComm != tt.want || gotPID != tt.pid || gotOK != tt.ok {
				t.Fatalf("parseSegfaultMessage() = (%q, %d, %t), want (%q, %d, %t)", gotComm, gotPID, gotOK, tt.want, tt.pid, tt.ok)
			}
		})
	}
}

func TestCollector_SegfaultsOnOneCoreEmitClusterEvent(t *testing.T) {
	sysRoot := writeTopology(t, 8)
	procRoot := t.TempDir()
	entries := make([]Entry, 5)
	for i := range entries {
		writeProcessor(t, procRoot, 100+i, 0)
		entries[i] = kernelSegfaultEntry(100+i, "node", "cursor-"+strconv.Itoa(i))
	}

	c := New()
	c.open = fakeOpen(entries)
	if err := c.Init(context.Background(), collector.Config{
		"cursor_path": filepath.Join(t.TempDir(), "cursor"),
		"sys_root":    sysRoot,
		"proc_root":   procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected one cluster event, got %+v", sink.events)
	}
	if got := sink.events[0].Attrs["comm"]; got != "node" {
		t.Fatalf("expected sanitized comm %q, got %q", "node", got)
	}
}

func TestCollector_DistributedSegfaultsDoNotEmitClusterEvent(t *testing.T) {
	sysRoot := writeTopology(t, 8)
	procRoot := t.TempDir()
	var entries []Entry
	for n := 0; n < 5; n++ {
		for cpu := 0; cpu < 8; cpu++ {
			pid := 1000 + cpu*10 + n
			writeProcessor(t, procRoot, pid, cpu)
			entries = append(entries, kernelSegfaultEntry(pid, "node", "cursor-"+strconv.Itoa(pid)))
		}
	}

	c := New()
	c.open = fakeOpen(entries)
	if err := c.Init(context.Background(), collector.Config{
		"cursor_path": filepath.Join(t.TempDir(), "cursor"),
		"sys_root":    sysRoot,
		"proc_root":   procRoot,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no cluster events for distributed faults, got %+v", sink.events)
	}
}

func kernelSegfaultEntry(pid int, comm, cursor string) Entry {
	entry := sampleEntry(comm+"["+strconv.Itoa(pid)+"]: segfault at 0 ip 1 sp 2 error 6", "3", cursor)
	entry.Fields["_TRANSPORT"] = "kernel"
	return entry
}

func writeTopology(t *testing.T, cpus int) string {
	t.Helper()
	sysRoot := t.TempDir()
	for cpu := 0; cpu < cpus; cpu++ {
		cpuPath := filepath.Join(sysRoot, "devices", "system", "cpu", "cpu"+strconv.Itoa(cpu), "topology")
		if err := os.MkdirAll(cpuPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cpuPath, "core_id"), []byte(strconv.Itoa(cpu)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return sysRoot
}

func writeProcessor(t *testing.T, procRoot string, pid, cpu int) {
	t.Helper()
	statPath := filepath.Join(procRoot, strconv.Itoa(pid), "stat")
	if err := os.MkdirAll(filepath.Dir(statPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 37)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "R"
	fields[36] = strconv.Itoa(cpu)
	if err := os.WriteFile(statPath, []byte(strconv.Itoa(pid)+" (node worker) "+strings.Join(fields, " ")), 0o644); err != nil {
		t.Fatal(err)
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
