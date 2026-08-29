package extract

import (
	"bufio"
	"bytes"
	"encoding/json"

	"github.com/bitacora-dev/bitacora/internal/job"
)

// Rclone extracts stats from a `rclone ... --use-json-log` run: rclone's
// JSON log is one object per line, and the periodic/final stats line
// carries a "stats" sub-object (ADR-0010: "JSON estructurado, sin regex").
// Extract keeps the last stats object seen — the final, cumulative totals.
type Rclone struct{}

func (Rclone) Detect(cmdName string, args []string) bool {
	return cmdName == "rclone"
}

type rcloneLogLine struct {
	Stats *rcloneStats `json:"stats"`
}

type rcloneStats struct {
	Bytes     int64 `json:"bytes"`
	Checks    int64 `json:"checks"`
	Deletes   int64 `json:"deletes"`
	Errors    int64 `json:"errors"`
	Renames   int64 `json:"renames"`
	Transfers int64 `json:"transfers"`
}

func (Rclone) Extract(stdout, stderr []byte) (job.Stats, error) {
	var last *rcloneStats

	// rclone's JSON log goes to stderr by default (or stdout with
	// --log-file unset and --use-json-log); scan both rather than guess.
	for _, stream := range [][]byte{stdout, stderr} {
		scanner := bufio.NewScanner(bytes.NewReader(stream))
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var parsed rcloneLogLine
			if err := json.Unmarshal(line, &parsed); err != nil {
				// Not every line is JSON-parseable stats — rclone can emit
				// plain diagnostic text too. Skip, don't fail the job.
				continue
			}
			if parsed.Stats != nil {
				last = parsed.Stats
			}
		}
	}

	if last == nil {
		return job.Stats{}, nil
	}

	return job.Stats{
		"bytes_transferred": last.Bytes,
		"files_checked":     last.Checks,
		"files_deleted":     last.Deletes,
		"errors":            last.Errors,
		"files_renamed":     last.Renames,
		"files_transferred": last.Transfers,
	}, nil
}
