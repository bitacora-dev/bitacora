// SPDX-License-Identifier: Apache-2.0

// Package runstats defines the extractor contract bitacora-run uses to turn
// a wrapped command's raw output into canonical schema.JobStats (ADR-0010),
// and picks the right one for a given command line.
//
// An extractor never runs the command itself and never touches the
// network, storage or the agent socket — it's a pure function over the
// bytes bitacora-run already captured. That's what keeps it testable with
// nothing but a fixture file.
package runstats

import (
	"github.com/bitacora-dev/bitacora/internal/runstats/rclone"
	"github.com/bitacora-dev/bitacora/internal/runstats/rsync"
	"github.com/bitacora-dev/bitacora/internal/runstats/snapraid"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Extractor recognizes one tool's output and turns it into JobStats.
type Extractor interface {
	// Name identifies the extractor, e.g. "rclone", "rsync", "snapraid".
	Name() string

	// Detect reports whether argv (the wrapped command, argv[0] is the
	// binary) is output this extractor knows how to parse.
	Detect(argv []string) bool

	// Extract parses the command's captured stdout/stderr and exit code
	// into canonical stats. A parsing problem is a non-fatal error,
	// appended to errs — it must never abort bitacora-run, which has
	// already done its one required job of running the command.
	Extract(stdout, stderr []byte, exitCode int) (stats schema.JobStats, errs []string)
}

// builtin lists extractors in detection priority order, checked before
// falling back to Generic.
var builtin = []Extractor{
	rclone.Extractor{},
	rsync.Extractor{},
	snapraid.Extractor{},
}

// For returns the extractor whose Detect matches argv, or Generic if none
// do — an empty argv always gets Generic.
func For(argv []string) Extractor {
	for _, e := range builtin {
		if e.Detect(argv) {
			return e
		}
	}
	return Generic{}
}
