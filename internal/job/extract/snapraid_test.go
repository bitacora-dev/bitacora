package extract

import "testing"

func TestSnapRAID_Detect(t *testing.T) {
	s := SnapRAID{}
	if !s.Detect("snapraid", []string{"sync"}) {
		t.Fatal("expected snapraid sync to be detected")
	}
	if !s.Detect("snapraid", []string{"scrub"}) {
		t.Fatal("expected snapraid scrub to be detected")
	}
	if s.Detect("snapraid", []string{"status"}) {
		t.Fatal("expected snapraid status not to be detected (nothing to extract)")
	}
	if s.Detect("snapraid", nil) {
		t.Fatal("expected snapraid with no subcommand not to be detected")
	}
	if s.Detect("rsync", []string{"sync"}) {
		t.Fatal("expected a different command not to be detected")
	}
}

func TestSnapRAID_ExtractsSuccess(t *testing.T) {
	output := "Selecting...\nSyncing...\nSaving state...\nVerifying...\nEverything OK\n"

	stats, err := SnapRAID{}.Extract([]byte(output), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["completed_ok"] != true {
		t.Fatalf("expected completed_ok=true, got %v", stats)
	}
	if _, ok := stats["errors"]; ok {
		t.Fatalf("expected no errors key on a clean run, got %v", stats)
	}
}

func TestSnapRAID_ExtractsErrorCount(t *testing.T) {
	output := "Selecting...\nSyncing...\nWARNING! There are errors!\n3 errors\n"

	stats, err := SnapRAID{}.Extract([]byte(output), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["completed_ok"] != false {
		t.Fatalf("expected completed_ok=false, got %v", stats)
	}
	if stats["errors"] != int64(3) {
		t.Fatalf("expected errors=3, got %v", stats["errors"])
	}
}
