// Package snapraid extracts schema.JobStats from a SnapRAID `sync` or
// `scrub` run (ADR-0010): both print a fixed-format summary block of
// "<count> <label>" lines just before the final "Everything OK" (or an
// error).
package snapraid

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Extractor parses SnapRAID's `sync`/`scrub` summary output.
type Extractor struct{}

// Name implements runstats.Extractor.
func (Extractor) Name() string { return "snapraid" }

// Detect implements runstats.Extractor.
func (Extractor) Detect(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return path.Base(argv[0]) == "snapraid"
}

// summaryLine matches SnapRAID's "<count> <label>" summary rows, e.g.
// "    219 equal", "     12 added", "      0 CRC errors".
var summaryLine = regexp.MustCompile(`^\s*(\d+)\s+([A-Za-z][A-Za-z0-9 _/\-]*)$`)

// changed are sync labels counted as files_transferred: anything that made
// the array's content differ from before.
var changed = map[string]bool{"added": true, "updated": true, "copied": true, "restored": true}

// checked are every label counted toward files_checked: the full set of
// entries sync/scrub reasoned about, changed or not.
var checked = map[string]bool{
	"equal": true, "added": true, "removed": true, "updated": true,
	"moved": true, "copied": true, "restored": true,
}

// Extract implements runstats.Extractor.
func (Extractor) Extract(stdout, stderr []byte, exitCode int) (schema.JobStats, []string) {
	var transferred, checkedCount, deleted, errCount int64
	found := false

	for _, buf := range [][]byte{stdout, stderr} {
		scanner := bufio.NewScanner(bytes.NewReader(buf))
		for scanner.Scan() {
			m := summaryLine.FindStringSubmatch(scanner.Text())
			if m == nil {
				continue
			}
			n, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				continue
			}
			label := strings.ToLower(strings.TrimSpace(m[2]))
			found = true

			switch {
			case label == "removed":
				deleted += n
				checkedCount += n
			case checked[label]:
				checkedCount += n
				if changed[label] {
					transferred += n
				}
			case strings.Contains(label, "error"):
				errCount += n
			}
		}
	}

	var errs []string
	if !found {
		errs = append(errs, "snapraid: no summary lines found in output")
	}

	return schema.JobStats{
		schema.StatFilesTransferred: transferred,
		schema.StatFilesDeleted:     deleted,
		schema.StatFilesChecked:     checkedCount,
		schema.StatErrors:           errCount,
	}, errs
}
