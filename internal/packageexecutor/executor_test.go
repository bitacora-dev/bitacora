package packageexecutor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessRejectsUnlistedOperationWithoutRunningCommand(t *testing.T) {
	dir := t.TempDir()
	requestDir := filepath.Join(dir, "requests")
	resultDir := filepath.Join(dir, "results")
	if err := Enqueue(requestDir, Request{ID: "request-1", Operation: RefreshPackageCache, HostID: "host-a"}); err != nil {
		t.Fatal(err)
	}

	called := false
	processed, err := ProcessAll(context.Background(), Config{
		RequestDir: requestDir,
		ResultDir:  resultDir,
		Allowlist:  Allowlist{},
	}, func(context.Context, Operation) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if called {
		t.Fatal("unlisted operation ran a command")
	}
	result := readResult(t, resultDir, "request-1")
	if result.Status != StatusFailed || result.ExitCode == 0 || result.Output == "" {
		t.Fatalf("unexpected rejected result: %+v", result)
	}
}

func TestProcessApplyRejectsStaleCacheWithoutRunningCommand(t *testing.T) {
	dir := t.TempDir()
	requestDir := filepath.Join(dir, "requests")
	resultDir := filepath.Join(dir, "results")
	stamp := filepath.Join(dir, "update-success-stamp")
	if err := os.WriteFile(stamp, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(requestDir, Request{ID: "request-2", Operation: ApplyPendingPackageUpdates, HostID: "host-a"}); err != nil {
		t.Fatal(err)
	}

	called := false
	_, err := ProcessAll(context.Background(), Config{
		RequestDir:     requestDir,
		ResultDir:      resultDir,
		Allowlist:      Allowlist{ApplyPendingPackageUpdates: true},
		CacheStampPath: stamp,
		MaxCacheAge:    time.Hour,
		Now:            time.Now,
	}, func(context.Context, Operation) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("stale cache ran package update")
	}
	result := readResult(t, resultDir, "request-2")
	if result.Status != StatusFailed || result.ExitCode == 0 || result.Output == "" {
		t.Fatalf("unexpected stale-cache result: %+v", result)
	}
}

func TestProcessRunsOnlyFixedOperationAndRecordsFailureOutput(t *testing.T) {
	dir := t.TempDir()
	requestDir := filepath.Join(dir, "requests")
	resultDir := filepath.Join(dir, "results")
	if err := Enqueue(requestDir, Request{ID: "request-3", Operation: RefreshPackageCache, HostID: "host-a"}); err != nil {
		t.Fatal(err)
	}

	var got Operation
	_, err := ProcessAll(context.Background(), Config{
		RequestDir: requestDir,
		ResultDir:  resultDir,
		Allowlist:  Allowlist{RefreshPackageCache: true},
	}, func(_ context.Context, operation Operation) ([]byte, int, error) {
		got = operation
		return []byte("repository unavailable\n"), 100, errors.New("exit status 100")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != RefreshPackageCache {
		t.Fatalf("operation = %q", got)
	}
	result := readResult(t, resultDir, "request-3")
	if result.Status != StatusFailed || result.ExitCode != 100 || result.Output != "repository unavailable\n" {
		t.Fatalf("unexpected failed result: %+v", result)
	}
}

func readResult(t *testing.T, dir, id string) Result {
	t.Helper()
	result, err := ReadResult(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
