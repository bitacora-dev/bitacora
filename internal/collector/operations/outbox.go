// Package operations imports completed iCloudServer operation events from the
// append-only outbox. It never writes to the producer's log; its small cursor
// lives beside Bitacora state and survives restart and log rotation.
package operations

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	DefaultOutboxPath   = "/var/log/icloudserver/operations/backup-events.jsonl"
	DefaultSchedulePath = "/etc/icloudserver/backup-observability.json"
)

type cursor struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

// Collector tails the operations outbox. Path and CursorPath are exported so
// deployments and tests can relocate only Bitacora-owned state.
type Collector struct{ Path, CursorPath, SchedulePath, hostID string }

func New() *Collector {
	return &Collector{Path: DefaultOutboxPath, CursorPath: "/var/lib/bitacora/cursors/backup-events.json", SchedulePath: DefaultSchedulePath}
}
func (c *Collector) Name() string                     { return "operations" }
func (c *Collector) Requires() []collector.Capability { return nil }
func (c *Collector) Init(_ context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.hostID = host.ID
	if v, ok := cfg["outbox_path"].(string); ok && v != "" {
		c.Path = v
	}
	if v, ok := cfg["cursor_path"].(string); ok && v != "" {
		c.CursorPath = v
	}
	if v, ok := cfg["schedule_path"].(string); ok && v != "" {
		c.SchedulePath = v
	}
	return nil
}
func (c *Collector) Close() error { return nil }

func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	f, err := os.Open(c.Path) // read-only: the outbox belongs to its producer.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening operations outbox: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	cur := c.loadCursor()
	inode := uint64(info.Sys().(*syscall.Stat_t).Ino)
	if cur.Inode != inode || cur.Offset > info.Size() {
		cur.Offset = 0
	}
	if _, err := f.Seek(cur.Offset, 0); err != nil {
		return err
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1024*1024)
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event outboxEvent
		if err := json.Unmarshal(s.Bytes(), &event); err == nil && event.EventType == "completed" && event.EventID != "" && !event.RecordedAt.IsZero() {
			if jobs, ok := sink.(interface{ Job(schema.Job) }); ok {
				jobs.Job(event.job(c.hostID, c.nextExpected(event.RecordedAt)))
			}
		}
		// overdue and timer_unavailable are not Job records yet. They need a
		// canonical ADR-0010 event mapping before they can be surfaced safely;
		// until then they are deliberately ignored rather than forged as jobs.
		cur.Offset += int64(len(s.Bytes()) + 1)
	}
	if err := s.Err(); err != nil {
		return err
	}
	return c.saveCursor(cursor{Inode: inode, Offset: cur.Offset})
}

type outboxEvent struct {
	EventType     string    `json:"event_type"`
	Unit          string    `json:"unit"`
	Status        string    `json:"status"`
	ServiceResult string    `json:"service_result"`
	ExitStatus    int       `json:"exit_status"`
	EventID       string    `json:"event_id"`
	RecordedAt    time.Time `json:"recorded_at"`
	DurationMS    int64     `json:"duration_ms"`
}

func (e outboxEvent) job(hostID string, nextExpected time.Time) schema.Job {
	name := strings.TrimSuffix(e.Unit, ".service")
	return schema.Job{ID: e.EventID, JobName: name, HostID: hostID, FinishedAt: e.RecordedAt,
		StartedAt: e.RecordedAt.Add(-time.Duration(e.DurationMS) * time.Millisecond), DurationSecond: float64(e.DurationMS) / 1000,
		Status: status(e.ExitStatus), ExitCode: e.ExitStatus, Trigger: "systemd-timer", NextExpected: nextExpected, Schema: schema.CurrentSchemaVersion}
}
func status(exit int) schema.JobStatus {
	if exit == 0 {
		return schema.JobSuccess
	}
	return schema.JobFailed
}

type schedulePolicy struct {
	Timezone string   `json:"timezone"`
	Times    []string `json:"times"`
}

// nextExpected computes the first configured local wall-clock time after at.
// An absent or malformed policy leaves NextExpected unknown (zero), rather
// than inventing a cadence.
func (c *Collector) nextExpected(at time.Time) time.Time {
	b, err := os.ReadFile(c.SchedulePath)
	if err != nil {
		return time.Time{}
	}
	var policy schedulePolicy
	if json.Unmarshal(b, &policy) != nil || len(policy.Times) == 0 {
		return time.Time{}
	}
	loc, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return time.Time{}
	}
	local := at.In(loc)
	var next time.Time
	for day := 0; day <= 1; day++ {
		for _, raw := range policy.Times {
			parsed, err := time.Parse("15:04", raw)
			if err != nil {
				continue
			}
			candidate := time.Date(local.Year(), local.Month(), local.Day()+day, parsed.Hour(), parsed.Minute(), 0, 0, loc)
			if candidate.After(local) && (next.IsZero() || candidate.Before(next)) {
				next = candidate
			}
		}
	}
	return next.UTC()
}
func (c *Collector) loadCursor() cursor {
	b, err := os.ReadFile(c.CursorPath)
	if err != nil {
		return cursor{}
	}
	var cur cursor
	_ = json.Unmarshal(b, &cur)
	return cur
}
func (c *Collector) saveCursor(cur cursor) error {
	if err := os.MkdirAll(filepath.Dir(c.CursorPath), 0o750); err != nil {
		return err
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	return os.WriteFile(c.CursorPath, b, 0o600)
}
