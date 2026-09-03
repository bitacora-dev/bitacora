// SPDX-License-Identifier: Apache-2.0

package jobwriter

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

func testJob() schema.Job {
	return schema.Job{
		ID:        "01J8XR000000000000000001",
		JobName:   "rclone-aginsur-sync",
		HostID:    "01J8X0000000000000000000",
		StartedAt: time.Now().UTC(),
		Status:    schema.JobSuccess,
		Schema:    schema.CurrentSchemaVersion,
	}
}

// TestWrite_AgentDown_FallsBackToSpool is the test ADR-0010 requires
// explicitly: with the agent stopped, the wrapped command still runs (that
// part is execwrap's job) and the job still shows up once the agent comes
// back — meaning it must land in the spool now.
func TestWrite_AgentDown_FallsBackToSpool(t *testing.T) {
	dir := t.TempDir()
	w := New(filepath.Join(dir, "does-not-exist.sock"), filepath.Join(dir, "jobs"))

	job := testJob()
	usedSocket, err := w.Write(job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usedSocket {
		t.Fatal("expected the spool fallback, not the socket")
	}

	entries, err := spool.ReadDir(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("reading spool dir: %v", err)
	}
	entry, ok := entries[job.ID]
	if !ok {
		t.Fatalf("expected job %s to appear in the spool, got entries %v", job.ID, entries)
	}

	var got schema.Job
	if err := json.Unmarshal(entry.Data, &got); err != nil {
		t.Fatalf("unmarshaling spooled job: %v", err)
	}
	if got.ID != job.ID || got.JobName != job.JobName {
		t.Errorf("spooled job = %+v, want %+v", got, job)
	}
}

func TestWrite_AgentReachable_UsesSocket(t *testing.T) {
	dir := t.TempDir()

	// Unix socket paths are limited to ~104-108 bytes depending on the
	// OS; t.TempDir()'s path embeds the full test name and can blow past
	// that, so the socket itself lives in a short-named directory of its
	// own instead.
	sockDir, err := os.MkdirTemp("", "bcjw")
	if err != nil {
		t.Fatalf("creating short-named socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "agent.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listening on test socket: %v", err)
	}
	defer ln.Close()

	received := make(chan schema.Job, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var job schema.Job
		if err := json.Unmarshal(line, &job); err == nil {
			received <- job
		}
	}()

	w := New(sockPath, filepath.Join(dir, "jobs"))
	job := testJob()
	usedSocket, err := w.Write(job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !usedSocket {
		t.Fatal("expected the socket path to be used")
	}

	select {
	case got := <-received:
		if got.ID != job.ID {
			t.Errorf("received job ID = %q, want %q", got.ID, job.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the job on the socket")
	}

	entries, err := spool.ReadDir(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("reading spool dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no spool entries when the socket succeeded, got %v", entries)
	}
}
