package sqlitetokenstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("opening token store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("closing token store: %v", err)
		}
	})
	return store, path
}

func TestStore_LookupReturnsBoundHostID(t *testing.T) {
	store, _ := newTestStore(t)

	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hostID, ok, err := store.Lookup(context.Background(), "token-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || hostID != "host-a" {
		t.Fatalf("expected host-a, got hostID=%q ok=%v", hostID, ok)
	}
}

func TestStore_LookupRejectsUnknownToken(t *testing.T) {
	store, _ := newTestStore(t)

	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, ok, err := store.Lookup(context.Background(), "no-such-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected an unknown token not to resolve")
	}
}

func TestStore_LookupDoesNotConfuseHosts(t *testing.T) {
	store, _ := newTestStore(t)

	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.AddToken("host-b", "token-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hostID, ok, err := store.Lookup(context.Background(), "token-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || hostID != "host-b" {
		t.Fatalf("expected host-b, got hostID=%q ok=%v", hostID, ok)
	}
}

func TestStore_PersistsTokensAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")

	store, err := New(path)
	if err != nil {
		t.Fatalf("opening token store: %v", err)
	}
	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing token store: %v", err)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopening token store: %v", err)
	}
	defer reopened.Close()

	hostID, ok, err := reopened.Lookup(context.Background(), "token-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || hostID != "host-a" {
		t.Fatalf("expected persisted token for host-a, got hostID=%q ok=%v", hostID, ok)
	}
}

func TestStore_DoesNotStorePlaintextToken(t *testing.T) {
	store, path := newTestStore(t)

	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	defer db.Close()

	var stored string
	if err := db.QueryRow(`SELECT token_hash FROM ingest_tokens WHERE host_id = ?`, "host-a").Scan(&stored); err != nil {
		t.Fatalf("reading stored hash: %v", err)
	}
	if stored == "token-a" {
		t.Fatal("expected token hash, got plaintext token")
	}
}
