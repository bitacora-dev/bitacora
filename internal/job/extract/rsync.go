package extract

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/job"
)

// Rsync extracts stats from a `rsync ... --stats` run's textual summary
// block (ADR-0010's table: "sobre la salida"). rsync doesn't offer a
// structured output format, so this is a line parser against its
// documented --stats field names, tolerant of the thousands-separator
// commas rsync prints by default.
type Rsync struct{}

func (Rsync) Detect(cmdName string, args []string) bool {
	if cmdName != "rsync" {
		return false
	}
	for _, a := range args {
		if a == "--stats" {
			return true
		}
	}
	return false
}

var (
	rsyncNumFiles       = regexp.MustCompile(`(?im)^Number of files:\s*([\d,]+)`)
	rsyncNumTransferred = regexp.MustCompile(`(?im)^Number of (?:regular files transferred|files transferred):\s*([\d,]+)`)
	rsyncNumDeleted     = regexp.MustCompile(`(?im)^Number of deleted files:\s*([\d,]+)`)
	rsyncTotalXferSize  = regexp.MustCompile(`(?im)^Total transferred file size:\s*([\d,]+)`)
)

// Extract parses rsync's --stats block. It's best-effort by design: rsync
// has no machine-readable output mode, and field wording has drifted
// slightly across versions in the past. A field this regex doesn't find is
// simply absent from Stats rather than treated as a parse failure — an
// unrecognized rsync build still produces a usable, if smaller, Job.
func (Rsync) Extract(stdout, stderr []byte) (job.Stats, error) {
	text := string(stdout) + "\n" + string(stderr)
	stats := job.Stats{}

	if m := rsyncNumFiles.FindStringSubmatch(text); m != nil {
		if n, err := parseThousands(m[1]); err == nil {
			stats["files_checked"] = n
		}
	}
	if m := rsyncNumTransferred.FindStringSubmatch(text); m != nil {
		if n, err := parseThousands(m[1]); err == nil {
			stats["files_transferred"] = n
		}
	}
	if m := rsyncNumDeleted.FindStringSubmatch(text); m != nil {
		if n, err := parseThousands(m[1]); err == nil {
			stats["files_deleted"] = n
		}
	}
	if m := rsyncTotalXferSize.FindStringSubmatch(text); m != nil {
		if n, err := parseThousands(m[1]); err == nil {
			stats["bytes_transferred"] = n
		}
	}

	stats["errors"] = countRsyncErrorLines(stderr)

	return stats, nil
}

func parseThousands(s string) (int64, error) {
	return strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
}

// countRsyncErrorLines counts stderr lines rsync itself prefixes as errors.
// rsync's --stats block has no "errors" field, unlike rclone's JSON stats —
// this is a documented approximation, not an exact count.
func countRsyncErrorLines(stderr []byte) int64 {
	var n int64
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "rsync: ") || strings.HasPrefix(line, "rsync error:") {
			n++
		}
	}
	return n
}
