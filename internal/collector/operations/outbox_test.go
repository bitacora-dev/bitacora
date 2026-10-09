package operations

import (
	"context"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sink struct{ jobs []schema.Job }

func (s *sink) Gauge(string, float64, collector.Labels)   {}
func (s *sink) Counter(string, float64, collector.Labels) {}
func (s *sink) Event(collector.Event)                     {}
func (s *sink) LogLines(string, []collector.LogLine)      {}
func (s *sink) Inventory(collector.Inventory)             {}
func (s *sink) Job(j collector.Job)                       { s.jobs = append(s.jobs, j) }

func TestCollect_UnchangedOutboxDoesNotReemitEventID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(`{"event_type":"completed","event_id":"once","unit":"backup.service","status":"success","service_result":"success","exit_status":0,"recorded_at":"2026-09-07T12:00:00Z","duration_ms":250}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New()
	c.Path = path
	c.CursorPath = filepath.Join(dir, "cursor.json")
	if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 || s.jobs[0].ID != "once" {
		t.Fatalf("unchanged outbox re-emitted event_id: %+v", s.jobs)
	}
}

func TestCollect_CompletedOutboxRecordMapsCanonicalJob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup-events.jsonl")
	policy := filepath.Join(dir, "backup-observability.json")
	// This is the producer contract: event_type/unit/status/service_result and
	// numeric exit_status; exit_code is intentionally not accepted.
	line := `{"event_type":"completed","event_id":"01JREAL","unit":"icloud-backup.service","status":"success","service_result":"success","exit_status":0,"duration_ms":1250,"recorded_at":"2026-09-07T01:30:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte(`{"timezone":"Europe/Madrid","times":["03:00","16:00"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New()
	c.Path = path
	c.CursorPath = filepath.Join(dir, "cursor.json")
	c.SchedulePath = policy
	if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 {
		t.Fatalf("expected one Job, got %+v", s.jobs)
	}
	got := s.jobs[0]
	finished := time.Date(2026, 9, 7, 1, 30, 0, 0, time.UTC)
	started := finished.Add(-1250 * time.Millisecond)
	next := time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC) // 16:00 Europe/Madrid
	if got.JobName != "icloud-backup" || got.ExitCode != 0 || !got.FinishedAt.Equal(finished) || !got.StartedAt.Equal(started) || got.DurationSecond != 1.25 || got.Trigger != "systemd-timer" || !got.NextExpected.Equal(next) || got.Stats != nil {
		t.Fatalf("unexpected Job: %+v", got)
	}
}

func TestCollect_NonCompletedTypesDoNotCreateJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	lines := `{"event_type":"overdue","event_id":"overdue","unit":"backup.service","recorded_at":"2026-09-07T12:00:00Z"}` + "\n" + `{"event_type":"timer_unavailable","event_id":"timer","unit":"backup.service","recorded_at":"2026-09-07T12:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New()
	c.Path = path
	c.CursorPath = filepath.Join(dir, "cursor.json")
	if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 0 {
		t.Fatalf("non-completed events became jobs: %+v", s.jobs)
	}
}

func TestCollect_RotationStartsAtNewFile(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "events.jsonl")
	cursor := filepath.Join(d, "cursor.json")
	c := New()
	c.Path = path
	c.CursorPath = cursor
	if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"event_type":"completed","event_id":"one","unit":"backup.service","exit_status":0,"recorded_at":"2026-09-07T12:00:00Z","duration_ms":250}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 || s.jobs[0].StartedAt.Format("15:04:05") != "11:59:59" {
		t.Fatalf("unexpected job: %+v", s.jobs)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"event_type":"completed","event_id":"two","unit":"backup.service","exit_status":2,"recorded_at":"2026-09-07T13:00:00Z","duration_ms":1000}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Collect(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 2 || s.jobs[1].ID != "two" || s.jobs[1].Status != schema.JobFailed {
		t.Fatalf("rotation did not import new outbox: %+v", s.jobs)
	}
}
