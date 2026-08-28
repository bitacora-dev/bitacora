package schema

import (
	"path/filepath"
	"testing"
)

func TestLoadOrCreateHostID_CreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host_id")

	id, err := LoadOrCreateHostID(path)
	if err != nil {
		t.Fatalf("unexpected error creating host_id: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty host_id")
	}
	if len(id) != 26 { // ULID string length
		t.Fatalf("expected a 26-char ULID, got %q (%d chars)", id, len(id))
	}
}

func TestLoadOrCreateHostID_IsStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host_id")

	first, err := LoadOrCreateHostID(path)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	second, err := LoadOrCreateHostID(path)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	if first != second {
		t.Fatalf("expected the same host_id across calls, got %q then %q", first, second)
	}
}

func TestLoadOrCreateHostID_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "var", "lib", "bitacora", "host_id")

	if _, err := LoadOrCreateHostID(path); err != nil {
		t.Fatalf("expected parent directories to be created, got %v", err)
	}
}
