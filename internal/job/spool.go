package job

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteSpool durably writes job as one file in dir, named after its ID, so
// several jobs can coexist (unlike internal/spool's one-file-per-collector
// contract, built for helpers that each own a single, overwritten entry).
// It writes to a temp file, fsyncs, then renames over the destination — a
// crash mid-write must never leave a corrupt or half-written file behind.
func WriteSpool(dir string, j Job) error {
	encoded, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling job %s for spool: %w", j.ID, err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating job spool dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".job-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp job spool file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing job spool entry: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsyncing job spool entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing job spool temp file: %w", err)
	}

	dest := filepath.Join(dir, j.ID+".json")
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming job spool entry into place: %w", err)
	}
	return nil
}

// SpooledJob pairs a decoded Job with the path it was read from, so a
// caller can remove it once delivered.
type SpooledJob struct {
	Path string
	Job  Job
}

// ReadSpool reads every *.json job in dir, oldest first by filename — job
// IDs are ULIDs, which sort chronologically. A file that fails to parse is
// skipped, not fatal: one corrupt entry shouldn't block draining the rest.
func ReadSpool(dir string) ([]SpooledJob, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading job spool dir %s: %w", dir, err)
	}

	var names []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		names = append(names, f.Name())
	}
	sort.Strings(names)

	var jobs []SpooledJob
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var j Job
		if err := json.Unmarshal(raw, &j); err != nil {
			continue
		}
		jobs = append(jobs, SpooledJob{Path: path, Job: j})
	}
	return jobs, nil
}

// RemoveSpool deletes a spooled job's file once it's been delivered.
// Removing an already-gone file is not an error — two backfill attempts
// racing on the same entry is harmless.
func RemoveSpool(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing job spool entry %s: %w", path, err)
	}
	return nil
}
