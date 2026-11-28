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

func TestCollectorRequestsPackageInventoryOnlyAfterSuccessfulApply(t *testing.T) {
	tests := []struct {
		name      string
		results   []packageexecutor.Result
		wantCalls int
	}{
		{
			name: "successful apply requests inventory collection",
			results: []packageexecutor.Result{{
				Request:   packageexecutor.Request{ID: "apply-success", HostID: "host-a", Operation: packageexecutor.ApplyPendingPackageUpdates},
				StartedAt: time.Unix(100, 0), FinishedAt: time.Unix(101, 0), Status: packageexecutor.StatusSuccess,
			}},
			wantCalls: 1,
		},
		{
			name: "failed apply does not request inventory collection",
			results: []packageexecutor.Result{{
				Request:   packageexecutor.Request{ID: "apply-failed", HostID: "host-a", Operation: packageexecutor.ApplyPendingPackageUpdates},
				StartedAt: time.Unix(100, 0), FinishedAt: time.Unix(101, 0), Status: packageexecutor.StatusFailed,
			}},
			wantCalls: 0,
		},
		{
			name: "cache refresh does not request inventory collection",
			results: []packageexecutor.Result{{
				Request:   packageexecutor.Request{ID: "cache-refresh", HostID: "host-a", Operation: packageexecutor.RefreshPackageCache},
				StartedAt: time.Unix(100, 0), FinishedAt: time.Unix(101, 0), Status: packageexecutor.StatusSuccess,
			}},
			wantCalls: 0,
		},
		{
			name: "two successful applies request one collection",
			results: []packageexecutor.Result{
				{Request: packageexecutor.Request{ID: "apply-one", HostID: "host-a", Operation: packageexecutor.ApplyPendingPackageUpdates}, StartedAt: time.Unix(100, 0), FinishedAt: time.Unix(101, 0), Status: packageexecutor.StatusSuccess},
				{Request: packageexecutor.Request{ID: "apply-two", HostID: "host-a", Operation: packageexecutor.ApplyPendingPackageUpdates}, StartedAt: time.Unix(102, 0), FinishedAt: time.Unix(103, 0), Status: packageexecutor.StatusSuccess},
			},
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, result := range tt.results {
				data, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, result.ID+".json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			calls := 0
			c := New(func(name string) {
				if name != "pkgupdates" {
					t.Errorf("requested collector %q, want pkgupdates", name)
				}
				calls++
			})
			c.ResultDir = dir
			if err := c.Init(context.Background(), nil, &collector.HostInfo{ID: "host-a"}); err != nil {
				t.Fatal(err)
			}
			if err := c.Collect(context.Background(), &recordingSink{}); err != nil {
				t.Fatal(err)
			}
			if calls != tt.wantCalls {
				t.Fatalf("collection requests = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}
