// Package packageactions imports terminal results written by the privileged
// ADR-0022 package helper. The agent only reads those files and sends jobs.
package packageactions

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/packageexecutor"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type Collector struct {
	ResultDir string
	hostID    string
}

func New() *Collector { return &Collector{ResultDir: packageexecutor.DefaultResultDir} }

func (c *Collector) Name() string                     { return "package-actions" }
func (c *Collector) Requires() []collector.Capability { return nil }
func (c *Collector) Close() error                     { return nil }

func (c *Collector) Init(_ context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.hostID = host.ID
	if value, ok := cfg["result_dir"].(string); ok && value != "" {
		c.ResultDir = value
	}
	return nil
}

func (c *Collector) Collect(_ context.Context, sink collector.Sink) error {
	entries, err := os.ReadDir(c.ResultDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.ResultDir, entry.Name())
		result, err := packageexecutor.ReadResult(path)
		if err != nil {
			continue
		}
		if !validResult(result) {
			continue
		}
		job := schema.Job{
			ID:             result.ID,
			JobName:        string(result.Operation),
			HostID:         c.hostID,
			StartedAt:      result.StartedAt,
			FinishedAt:     result.FinishedAt,
			DurationSecond: result.FinishedAt.Sub(result.StartedAt).Seconds(),
			Status:         terminalStatus(result.Status),
			ExitCode:       result.ExitCode,
			Trigger:        "systemd-path",
			Schema:         schema.CurrentSchemaVersion,
		}
		if jobs, ok := sink.(interface{ Job(schema.Job) }); ok {
			jobs.Job(job)
		}
		if result.Output != "" {
			sink.LogLines("package-action", outputLines(result, c.hostID))
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func validResult(result packageexecutor.Result) bool {
	return result.ID != "" && result.HostID != "" && !result.StartedAt.IsZero() && !result.FinishedAt.IsZero() &&
		(result.Operation == packageexecutor.RefreshPackageCache || result.Operation == packageexecutor.ApplyPendingPackageUpdates) &&
		(result.Status == packageexecutor.StatusSuccess || result.Status == packageexecutor.StatusFailed)
}

func terminalStatus(status packageexecutor.Status) schema.JobStatus {
	if status == packageexecutor.StatusSuccess {
		return schema.JobSuccess
	}
	return schema.JobFailed
}

func outputLines(result packageexecutor.Result, hostID string) []collector.LogLine {
	lines := strings.Split(strings.TrimSuffix(result.Output, "\n"), "\n")
	output := make([]collector.LogLine, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		output = append(output, collector.LogLine{TS: result.FinishedAt, HostID: hostID, Source: "package-action", UnitOrContainer: result.ID, Message: line})
	}
	return output
}

var _ collector.Collector = (*Collector)(nil)
