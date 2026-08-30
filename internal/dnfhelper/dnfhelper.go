// Package dnfhelper implements the bitacora-dnf helper's logic
// (ADR-0005, ADR-0017): run `dnf check-update` — a closed query with no
// parameters sourced from anything external (ADR-0012) — and parse its
// output into the list of packages with an update available. Command
// execution is injected so this is testable without dnf installed.
package dnfhelper

import (
	"bufio"
	"context"
	"strings"
)

// CommandRunner runs `dnf check-update` and returns its raw stdout, plus
// whether updates are available. dnf's own exit code 100 means "updates
// found" — not a failure — so the caller must distinguish that from a
// real error (dnf missing, timed out) explicitly rather than via err
// alone.
type CommandRunner func(ctx context.Context) (output []byte, updatesAvailable bool, err error)

// Update is one package dnf reports as having a newer version available.
type Update struct {
	Name    string `json:"name"`
	Arch    string `json:"arch"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
}

// Result is the "data" payload written into the spool entry.
type Result struct {
	Updates []Update `json:"updates"`
}

// Run executes the check and parses its output. A genuine failure (dnf
// missing, timed out — updatesAvailable false alongside a non-nil err) is
// reported as a non-fatal spool error, same as every other helper in this
// project: it never keeps a previous spool entry's stale data around by
// skipping the write.
func Run(ctx context.Context, run CommandRunner) (Result, []string) {
	out, updatesAvailable, err := run(ctx)
	if err != nil && !updatesAvailable {
		return Result{}, []string{"dnf check-update: " + err.Error()}
	}
	return Result{Updates: parseCheckUpdate(out)}, nil
}

// parseCheckUpdate reads `dnf check-update`'s plain-text table: one
// "name.arch    version    repo" line per updatable package. Parsing
// stops at the optional "Obsoleting Packages" section — packages that
// outright replace another installed package are a different concept
// from a version update, and that section's format includes an extra
// indented "obsoletes ..." continuation line this collector doesn't need
// to understand. Anything else that isn't exactly three
// whitespace-separated fields with a "name.arch" first field (headers,
// blank lines, the "Last metadata expiration check" banner) is skipped,
// not fatal.
func parseCheckUpdate(out []byte) []Update {
	var updates []Update
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Obsoleting Packages") {
			break
		}
		if line == "" || strings.HasPrefix(line, "Last metadata") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		name, arch, ok := splitNameArch(fields[0])
		if !ok {
			continue
		}
		updates = append(updates, Update{Name: name, Arch: arch, Version: fields[1], Repo: fields[2]})
	}
	return updates
}

// splitNameArch splits dnf's "name.arch" first column at the last '.' —
// package names can themselves contain dots, so the arch is always the
// final segment, never the first.
func splitNameArch(nameArch string) (name, arch string, ok bool) {
	idx := strings.LastIndexByte(nameArch, '.')
	if idx <= 0 || idx == len(nameArch)-1 {
		return "", "", false
	}
	return nameArch[:idx], nameArch[idx+1:], true
}
