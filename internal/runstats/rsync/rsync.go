// SPDX-License-Identifier: Apache-2.0

// Package rsync extracts schema.JobStats from an rsync run wrapped with
// `--stats` (ADR-0010): rsync has no structured output mode, so this parses
// the fixed-format summary block it prints at the end.
package rsync

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Extractor parses rsync's `--stats` summary and error lines.
type Extractor struct{}

// Name implements runstats.Extractor.
func (Extractor) Name() string { return "rsync" }

// Detect implements runstats.Extractor.
func (Extractor) Detect(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return path.Base(argv[0]) == "rsync"
}

// statLines maps the fixed labels rsync's `--stats` prints, in order of
// preference (newer rsync says "regular files transferred", older says
// just "files transferred"), to canonical JobStats keys.
var statLines = []struct {
	pattern *regexp.Regexp
	key     string
}{
	{regexp.MustCompile(`^Number of regular files transferred:\s*([\d,]+)`), schema.StatFilesTransferred},
	{regexp.MustCompile(`^Number of files transferred:\s*([\d,]+)`), schema.StatFilesTransferred},
	{regexp.MustCompile(`^Total transferred file size:\s*([\d,]+)`), schema.StatBytesTransferred},
	{regexp.MustCompile(`^Number of deleted files:\s*([\d,]+)`), schema.StatFilesDeleted},
	{regexp.MustCompile(`^Number of files:\s*([\d,]+)`), schema.StatFilesChecked},
}

// rsyncErrorPrefix marks a fatal transfer error rsync reports on stderr,
// distinct from the exit code alone (which can also reflect argument or
// protocol errors bitacora-run's own logging already captures verbatim).
var rsyncErrorPrefix = regexp.MustCompile(`^rsync(?:\[\d+\])?(?: error)?: `)

// Extract implements runstats.Extractor.
func (Extractor) Extract(stdout, stderr []byte, exitCode int) (schema.JobStats, []string) {
	stats := schema.JobStats{}
	found := false

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		for _, sl := range statLines {
			if _, already := stats[sl.key]; already {
				continue
			}
			if m := sl.pattern.FindStringSubmatch(line); m != nil {
				if n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64); err == nil {
					stats[sl.key] = n
					found = true
				}
			}
		}
	}

	errCount := int64(0)
	errScanner := bufio.NewScanner(bytes.NewReader(stderr))
	for errScanner.Scan() {
		if rsyncErrorPrefix.MatchString(errScanner.Text()) {
			errCount++
		}
	}
	stats[schema.StatErrors] = errCount

	var errs []string
	if !found {
		errs = append(errs, "rsync: no --stats summary found in output")
	}
	return stats, errs
}
