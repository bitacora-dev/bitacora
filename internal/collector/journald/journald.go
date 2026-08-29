// Package journald implements the systemd journal collector (ADR-0005,
// ADR-0007). Reading it needs no privilege beyond membership in the
// systemd-journal group (ADR-0005) — this package never execs journalctl
// and never runs as a helper.
//
// The real journal is only reachable through sdjournal, which requires
// CGO and libsystemd. That's a deliberate, scoped exception to ADR-0001's
// general "no CGO in the agent" rule — see reader_linux.go's doc comment.
// Everything else in this package (the Collect loop, cursor persistence,
// entry-to-LogLine conversion) is pure Go and tested against a fake
// Reader, so only reader_linux.go itself depends on cgo/libsystemd.
package journald

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Entry is one journal record, narrowed to the fields this collector uses.
type Entry struct {
	Fields       map[string]string
	RealtimeUsec uint64
	Cursor       string
}

// Reader abstracts the systemd journal so the collector's logic is
// testable without linking libsystemd.
type Reader interface {
	// Next advances to the next available entry. ok=false means nothing
	// new is available right now — normal for a live stream, not an error.
	Next(ctx context.Context) (entry Entry, ok bool, err error)
	Close() error
}

// OpenFunc opens a Reader starting just after cursor (empty cursor =
// start from the current tail, per ADR-0007: a collector's first run
// shouldn't replay the whole journal).
type OpenFunc func(cursor string) (Reader, error)

// maxEntriesPerCollect bounds one Collect call's work, consistent with
// the runtime's per-collector hard timeout (ADR-0007) — an arbitrarily
// long backlog is drained over several ticks, not one blocking call.
const maxEntriesPerCollect = 500

// DefaultCursorPath is where the persisted read position lives in
// production.
const DefaultCursorPath = "/var/lib/bitacora/journald.cursor"

// Collector emits log lines read from the systemd journal.
type Collector struct {
	open       OpenFunc
	cursorPath string
	hostID     string

	reader Reader
	cursor string
}

// New returns a collector that reads the real systemd journal (Linux
// only — see reader_linux.go).
func New() *Collector {
	return &Collector{open: openSystemdJournal}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "journald" }

// Requires implements collector.Collector.
func (c *Collector) Requires() []collector.Capability {
	return []collector.Capability{capabilities.LogsJournald}
}

// Init implements collector.Collector. cfg["cursor_path"] overrides where
// the persisted cursor lives; cfg["open_func"] (an OpenFunc) lets tests
// inject a fake journal reader instead of the real one.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	if host != nil {
		c.hostID = host.ID
	}

	c.cursorPath = DefaultCursorPath
	if v, ok := cfg["cursor_path"].(string); ok && v != "" {
		c.cursorPath = v
	}
	if v, ok := cfg["open_func"].(OpenFunc); ok && v != nil {
		c.open = v
	}

	if raw, err := os.ReadFile(c.cursorPath); err == nil {
		c.cursor = string(raw)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading cursor file %s: %w", c.cursorPath, err)
	}

	reader, err := c.open(c.cursor)
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}
	c.reader = reader
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	var lines []schema.LogLine
	lastCursor := c.cursor

	for i := 0; i < maxEntriesPerCollect; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entry, ok, err := c.reader.Next(ctx)
		if err != nil {
			return fmt.Errorf("reading journal entry: %w", err)
		}
		if !ok {
			break
		}

		lines = append(lines, c.entryToLogLine(entry))
		lastCursor = entry.Cursor
	}

	if len(lines) > 0 {
		sink.LogLines("journald", lines)
	}

	if lastCursor != "" && lastCursor != c.cursor {
		if err := persistCursor(c.cursorPath, lastCursor); err != nil {
			return fmt.Errorf("persisting cursor: %w", err)
		}
		c.cursor = lastCursor
	}

	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

func (c *Collector) entryToLogLine(e Entry) schema.LogLine {
	pid, _ := strconv.Atoi(e.Fields["_PID"])
	unit := e.Fields["_SYSTEMD_UNIT"]
	if unit == "" {
		unit = e.Fields["SYSLOG_IDENTIFIER"]
	}

	return schema.LogLine{
		TS:              time.UnixMicro(int64(e.RealtimeUsec)).UTC(),
		HostID:          c.hostID,
		Source:          "journald",
		UnitOrContainer: unit,
		Level:           priorityToLevel(e.Fields["PRIORITY"]),
		PID:             pid,
		Message:         e.Fields["MESSAGE"],
	}
}

var syslogLevels = map[string]string{
	"0": "emerg", "1": "alert", "2": "critical", "3": "error",
	"4": "warning", "5": "notice", "6": "info", "7": "debug",
}

func priorityToLevel(priority string) string {
	if level, ok := syslogLevels[priority]; ok {
		return level
	}
	return "info"
}

func persistCursor(path, cursor string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cursor-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(cursor); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
