package extract

import (
	"bytes"

	"github.com/bitacora-dev/bitacora/internal/job"
)

// Generic is the fallback extractor (ADR-0010's table: "genérico: duración,
// código de salida, líneas de salida"). Duration and exit code are already
// core Job fields set by the wrapper itself; the only thing left for an
// extractor to contribute is a line count, so that's all this does. It
// always succeeds — a command bitacora-run doesn't recognize must never
// fail the job over it.
type Generic struct{}

// Detect always matches: Generic is the extract.Select fallback, never
// registered for a specific command name.
func (Generic) Detect(cmdName string, args []string) bool { return false }

func (Generic) Extract(stdout, stderr []byte) (job.Stats, error) {
	return job.Stats{
		"stdout_lines": countLines(stdout),
		"stderr_lines": countLines(stderr),
	}, nil
}

func countLines(b []byte) int {
	b = bytes.TrimRight(b, "\n")
	if len(b) == 0 {
		return 0
	}
	return bytes.Count(b, []byte("\n")) + 1
}
