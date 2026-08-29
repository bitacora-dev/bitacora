// Package extract implements ADR-0010's per-command-type statistics
// extractors: given a wrapped command's name/args and its captured
// stdout+stderr, produce the job.Stats map. bitacora-run selects one via
// Select and always has a fallback (Generic), so an unrecognized command
// never fails the job — it just gets fewer stats.
package extract

import "github.com/bitacora-dev/bitacora/internal/job"

// Extractor turns one command's output into job.Stats.
type Extractor interface {
	// Detect reports whether this extractor applies to the given command.
	Detect(cmdName string, args []string) bool
	// Extract parses stdout/stderr into stats. A parse failure is returned
	// as an error, never a panic — Select's caller decides how to degrade.
	Extract(stdout, stderr []byte) (job.Stats, error)
}

// registry is checked in order; the first Detect match wins. Order matters
// only in that more specific extractors should be listed before more
// general ones, which isn't a concern yet since each detects a distinct
// command name.
var registry = []Extractor{
	Rclone{},
	Rsync{},
	SnapRAID{},
}

// Select returns the extractor matching cmdName/args, or Generic if none
// do. It never returns nil.
func Select(cmdName string, args []string) Extractor {
	for _, e := range registry {
		if e.Detect(cmdName, args) {
			return e
		}
	}
	return Generic{}
}
