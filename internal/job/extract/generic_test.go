package extract

import "testing"

func TestGeneric_CountsLines(t *testing.T) {
	stats, err := Generic{}.Extract([]byte("line1\nline2\nline3\n"), []byte("err1\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["stdout_lines"] != 3 {
		t.Fatalf("expected 3 stdout lines, got %v", stats["stdout_lines"])
	}
	if stats["stderr_lines"] != 1 {
		t.Fatalf("expected 1 stderr line, got %v", stats["stderr_lines"])
	}
}

func TestGeneric_EmptyOutputCountsZero(t *testing.T) {
	stats, err := Generic{}.Extract(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["stdout_lines"] != 0 || stats["stderr_lines"] != 0 {
		t.Fatalf("expected zero lines, got %v", stats)
	}
}

func TestSelect_FallsBackToGenericForUnknownCommand(t *testing.T) {
	e := Select("borg", []string{"create"})
	if _, ok := e.(Generic); !ok {
		t.Fatalf("expected Generic for an unrecognized command, got %T", e)
	}
}

func TestSelect_PicksTheMatchingExtractor(t *testing.T) {
	if _, ok := Select("rclone", []string{"sync"}).(Rclone); !ok {
		t.Fatal("expected Rclone for an rclone command")
	}
	if _, ok := Select("rsync", []string{"--stats"}).(Rsync); !ok {
		t.Fatal("expected Rsync for rsync --stats")
	}
	if _, ok := Select("snapraid", []string{"sync"}).(SnapRAID); !ok {
		t.Fatal("expected SnapRAID for snapraid sync")
	}
}
