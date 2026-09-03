// SPDX-License-Identifier: Apache-2.0

// Package rclone extracts schema.JobStats from an rclone run wrapped with
// `--use-json-log` (ADR-0010): structured JSON, no regex. rclone periodically
// logs a line carrying a "stats" object (via `--stats <interval>`); the last
// one seen is the final tally for the whole transfer.
package rclone

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Extractor parses rclone's `--use-json-log` output.
type Extractor struct{}

// Name implements runstats.Extractor.
func (Extractor) Name() string { return "rclone" }

// Detect implements runstats.Extractor.
func (Extractor) Detect(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return path.Base(argv[0]) == "rclone"
}

// logLine is one `--use-json-log` line. Only the fields we map are typed;
// everything else is discarded.
type logLine struct {
	Stats *rcloneStats `json:"stats"`
}

// rcloneStats mirrors the subset of rclone's core/stats object (see
// `rclone rc core/stats`) that maps to canonical JobStats fields.
type rcloneStats struct {
	Bytes     int64 `json:"bytes"`
	Checks    int64 `json:"checks"`
	Deletes   int64 `json:"deletes"`
	Errors    int64 `json:"errors"`
	Transfers int64 `json:"transfers"`
}

// Extract implements runstats.Extractor. It scans stdout and stderr (rclone
// logs to stderr by default) for JSON log lines and keeps the last one
// carrying a "stats" object as the final tally.
func (Extractor) Extract(stdout, stderr []byte, exitCode int) (schema.JobStats, []string) {
	var last *rcloneStats
	var errs []string

	for _, buf := range [][]byte{stdout, stderr} {
		for _, line := range bytes.Split(buf, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var ll logLine
			if err := json.Unmarshal(line, &ll); err != nil {
				errs = append(errs, "rclone: malformed json-log line: "+truncate(string(line), 120))
				continue
			}
			if ll.Stats != nil {
				last = ll.Stats
			}
		}
	}

	if last == nil {
		errs = append(errs, "rclone: no stats found in output; was --use-json-log --stats <interval> passed?")
		return schema.JobStats{}, errs
	}

	return schema.JobStats{
		schema.StatFilesTransferred: last.Transfers,
		schema.StatBytesTransferred: last.Bytes,
		schema.StatFilesDeleted:     last.Deletes,
		schema.StatFilesChecked:     last.Checks,
		schema.StatErrors:           last.Errors,
	}, errs
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
