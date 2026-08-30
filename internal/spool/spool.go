// Package spool implements the helper-to-agent exchange directory
// (ADR-0005): privileged helpers write one JSON file per run, atomically;
// the agent (or, for now, `bita doctor`) reads them and judges freshness.
package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is the on-disk shape every helper writes, verbatim from ADR-0005:
// { "collector": "smart", "ts": "...", "schema": 1, "data": {...}, "errors": [...] }
type Entry struct {
	Collector string          `json:"collector"`
	TS        time.Time       `json:"ts"`
	Schema    int             `json:"schema"`
	Data      json.RawMessage `json:"data"`
	Errors    []string        `json:"errors,omitempty"`
}

// Age is how long ago the entry was produced, relative to now.
func (e Entry) Age(now time.Time) time.Duration {
	return now.Sub(e.TS)
}

// Stale reports whether the entry is older than three times its expected
// collection interval — ADR-0005: "Un fichero con más de tres intervalos de
// antigüedad se descarta [...] un helper que ha dejado de ejecutarse debe
// ser visible, no invisible."
func (e Entry) Stale(now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	return e.Age(now) > 3*interval
}

// WriteAtomic writes one spool entry for collector, with the given schema
// version, arbitrary data (marshaled to JSON) and any non-fatal errors the
// helper hit. It writes to a temp file in dir, fsyncs, then renames over
// the destination — a crash mid-write must never leave a corrupt or
// half-written file the agent could misread.
func WriteAtomic(dir, collector string, schema int, data any, errs []string) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling spool data for %s: %w", collector, err)
	}

	entry := Entry{
		Collector: collector,
		TS:        time.Now().UTC(),
		Schema:    schema,
		Data:      payload,
		Errors:    errs,
	}

	encoded, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling spool entry for %s: %w", collector, err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating spool dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+collector+"-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp spool file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing spool entry: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsyncing spool entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing spool temp file: %w", err)
	}

	dest := filepath.Join(dir, collector+".json")
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming spool entry into place: %w", err)
	}
	return nil
}

// ReadDir reads every *.json spool entry in dir. A file that fails to parse
// is skipped, not fatal — one corrupt helper output shouldn't blind the
// caller to every other one.
func ReadDir(dir string) (map[string]Entry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return nil, fmt.Errorf("reading spool dir %s: %w", dir, err)
	}

	entries := make(map[string]Entry)
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		entries[e.Collector] = e
	}
	return entries, nil
}
