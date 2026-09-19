package packageactions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/packageexecutor"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type recordingSink struct {
	jobs []schema.Job
	logs []collector.LogLine
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) Inventory(collector.Inventory)             {}
func (s *recordingSink) LogLines(_ string, lines []collector.LogLine) {
	s.logs = append(s.logs, lines...)
}
func (s *recordingSink) Job(job schema.Job) { s.jobs = append(s.jobs, job) }

func TestCollectorEmitsTerminalJobAndOutput(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	result := packageexecutor.Result{
		Request:   packageexecutor.Request{ID: "request-1", HostID: "untrusted-host", Operation: packageexecutor.RefreshPackageCache},
		StartedAt: now.Add(-time.Second), FinishedAt: now, Status: packageexecutor.StatusFailed, ExitCode: 100, Output: "failed\n",
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request-1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	c := New()
	c.ResultDir = dir
	if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.jobs) != 1 || sink.jobs[0].Status != schema.JobFailed || sink.jobs[0].HostID != "host-a" || sink.jobs[0].ExitCode != 100 {
		t.Fatalf("jobs = %+v", sink.jobs)
	}
	if len(sink.logs) != 1 || sink.logs[0].Message != "failed" {
		t.Fatalf("logs = %+v", sink.logs)
	}
}
