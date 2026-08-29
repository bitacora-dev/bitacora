package hubapi

import (
	"context"
	"testing"
	"time"
)

func TestDeviceTokenStore_ClaimReturnsStartedToken(t *testing.T) {
	store := NewDeviceTokenStore()

	code, token, _, err := store.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := store.Claim(context.Background(), code)
	if !ok {
		t.Fatal("expected claim to succeed")
	}
	if got != token {
		t.Fatalf("expected claimed token %q, got %q", token, got)
	}
}

func TestDeviceTokenStore_ClaimIsSingleUse(t *testing.T) {
	store := NewDeviceTokenStore()

	code, _, _, err := store.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := store.Claim(context.Background(), code); !ok {
		t.Fatal("expected first claim to succeed")
	}
	if _, ok := store.Claim(context.Background(), code); ok {
		t.Fatal("expected second claim of the same code to fail")
	}
}

func TestDeviceTokenStore_ClaimRejectsUnknownCode(t *testing.T) {
	store := NewDeviceTokenStore()

	if _, ok := store.Claim(context.Background(), "no-such-code"); ok {
		t.Fatal("expected an unknown pairing code not to resolve")
	}
}

func TestDeviceTokenStore_ClaimRejectsExpiredCode(t *testing.T) {
	store := NewDeviceTokenStore()

	code, _, _, err := store.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store.pmu.Lock()
	store.pairings[code].expiresAt = time.Now().Add(-time.Second)
	store.pmu.Unlock()

	if _, ok := store.Claim(context.Background(), code); ok {
		t.Fatal("expected an expired pairing code not to resolve")
	}
}

func TestDeviceTokenStore_LookupAcceptsStartedToken(t *testing.T) {
	store := NewDeviceTokenStore()

	_, token, _, err := store.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ok, err := store.Lookup(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected the token minted by Start to be looked up successfully")
	}
}

func TestDeviceTokenStore_LookupRejectsUnknownToken(t *testing.T) {
	store := NewDeviceTokenStore()

	ok, err := store.Lookup(context.Background(), "not-a-real-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected an unknown token not to resolve")
	}
}
