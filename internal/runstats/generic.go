package runstats

import (
	"bytes"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Generic is the fallback extractor for any command with no dedicated
// extractor (ADR-0010): it reports only what's true of every process —
// output line counts and whether the exit code was non-zero.
type Generic struct{}

// Name implements Extractor.
func (Generic) Name() string { return "generic" }

// Detect implements Extractor. Generic never self-selects; runstats.For
// falls back to it when nothing else matches.
func (Generic) Detect(argv []string) bool { return false }

// Extract implements Extractor.
func (Generic) Extract(stdout, stderr []byte, exitCode int) (schema.JobStats, []string) {
	stats := schema.JobStats{
		"stdout_lines": countLines(stdout),
		"stderr_lines": countLines(stderr),
	}
	if exitCode != 0 {
		stats[schema.StatErrors] = 1
	}
	return stats, nil
}

func countLines(b []byte) int {
	b = bytes.TrimRight(b, "\n")
	if len(b) == 0 {
		return 0
	}
	return bytes.Count(b, []byte("\n")) + 1
}
