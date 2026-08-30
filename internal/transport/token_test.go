package transport

import (
	"strings"
	"testing"
)

func TestHashAndVerifyToken_RoundTrip(t *testing.T) {
	hash, err := HashToken("super-secret-token")
	if err != nil {
		t.Fatalf("unexpected error hashing: %v", err)
	}

	ok, err := VerifyToken("super-secret-token", hash)
	if err != nil {
		t.Fatalf("unexpected error verifying: %v", err)
	}
	if !ok {
		t.Fatal("expected the correct token to verify")
	}
}

func TestVerifyToken_RejectsWrongToken(t *testing.T) {
	hash, err := HashToken("super-secret-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ok, err := VerifyToken("wrong-token", hash)
	if err != nil {
		t.Fatalf("unexpected error verifying: %v", err)
	}
	if ok {
		t.Fatal("expected a wrong token not to verify")
	}
}

func TestHashToken_NeverStoresPlaintext(t *testing.T) {
	hash, err := HashToken("super-secret-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(hash, "super-secret-token") {
		t.Fatalf("hash must never contain the plaintext token: %s", hash)
	}
}

func TestHashToken_SaltsDifferently(t *testing.T) {
	a, err := HashToken("same-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := HashToken("same-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == b {
		t.Fatal("expected two hashes of the same token to differ (random salt)")
	}
}

func TestVerifyToken_RejectsMalformedHash(t *testing.T) {
	if _, err := VerifyToken("token", "not-a-valid-hash"); err == nil {
		t.Fatal("expected an error for a malformed stored hash")
	}
}
