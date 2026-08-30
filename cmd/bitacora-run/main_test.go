package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/spool"
)

func runArgs(t *testing.T, spoolDir string, extra []string) []string {
	t.Helper()
	base := []string{
		"--job", "test-job",
		"--socket", filepath.Join(spoolDir, "does-not-exist.sock"),
		"--spool-dir", filepath.Join(spoolDir, "jobs"),
		"--host-id-file", filepath.Join(spoolDir, "host_id"),
	}
	return append(base, extra...)
}

func TestRun_PropagatesExitCode(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := runArgs(t, dir, []string{"--", "sh", "-c", "exit 3"})
	code := run(args, &stdout, &stderr)
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestRun_PassthroughsOutput(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := runArgs(t, dir, []string{"--", "sh", "-c", "echo hello"})
	code := run(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := stdout.String(); got != "hello\n" {
		t.Errorf("stdout = %q, want %q", got, "hello\n")
	}
}

func TestRun_RecordsJobInSpool(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := runArgs(t, dir, []string{"--", "sh", "-c", "echo ok"})
	code := run(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	entries, err := spool.ReadDir(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("reading spool dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 job in the spool, got %d: %v", len(entries), entries)
	}

	var got map[string]any
	for _, e := range entries {
		if err := json.Unmarshal(e.Data, &got); err != nil {
			t.Fatalf("unmarshaling spooled job: %v", err)
		}
	}
	if got["job_name"] != "test-job" {
		t.Errorf("job_name = %v, want %q", got["job_name"], "test-job")
	}
	if got["status"] != "success" {
		t.Errorf("status = %v, want %q", got["status"], "success")
	}
}

func TestRun_MissingJobFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--", "sh", "-c", "echo hi"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRun_MissingCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--job", "x"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRun_BadBinary_StillFails(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := runArgs(t, dir, []string{"--", "this-binary-does-not-exist-anywhere"})
	code := run(args, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected an error message on stderr")
	}
}
