package transport

import (
	"context"
	"testing"
)

func TestMemoryTokenStore_LookupReturnsBoundHostID(t *testing.T) {
	store := NewMemoryTokenStore()
	if err := store.AddToken("host-a", "token-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.AddToken("host-b", "token-b"); err != nil {
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

func TestMemoryTokenStore_LookupRejectsUnknownToken(t *testing.T) {
	store := NewMemoryTokenStore()
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
