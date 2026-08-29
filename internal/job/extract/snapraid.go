package extract

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/job"
)

// SnapRAID extracts stats from `snapraid sync` / `snapraid scrub` output
// (ADR-0010's table: "parseo de sync/scrub"). SnapRAID, like rsync, has no
// structured output mode. This targets the two signals its transcripts
// reliably contain — the literal "Everything OK" success line, and an
// "N error(s)" count on failure — and is intentionally conservative: it's
// lower-confidence than the rclone/rsync extractors and should be treated
// as best-effort, to be tightened against real SnapRAID output once the
// project actually runs against a SnapRAID array.
type SnapRAID struct{}

func (SnapRAID) Detect(cmdName string, args []string) bool {
	if cmdName != "snapraid" {
		return false
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "sync", "scrub":
		return true
	default:
		return false
	}
}

var snapraidErrorCount = regexp.MustCompile(`(?im)(\d+)\s+errors?\b`)

func (SnapRAID) Extract(stdout, stderr []byte) (job.Stats, error) {
	text := string(stdout) + "\n" + string(stderr)

	stats := job.Stats{
		"completed_ok": strings.Contains(text, "Everything OK"),
	}

	if matches := snapraidErrorCount.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		last := matches[len(matches)-1]
		if n, err := strconv.ParseInt(last[1], 10, 64); err == nil {
			stats["errors"] = n
		}
	}

	return stats, nil
}
