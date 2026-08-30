package transport

import "testing"

func TestPerTokenLimiter_AllowsWithinBurst(t *testing.T) {
	l := NewPerTokenLimiter(1, 3)
	for i := 0; i < 3; i++ {
		if !l.Allow("host-a") {
			t.Fatalf("expected request %d to be allowed within burst", i)
		}
	}
}

func TestPerTokenLimiter_RejectsBeyondBurst(t *testing.T) {
	l := NewPerTokenLimiter(0.001, 2) // near-zero refill rate so the 3rd call can't succeed by refill
	l.Allow("host-a")
	l.Allow("host-a")
	if l.Allow("host-a") {
		t.Fatal("expected the request beyond burst to be rejected")
	}
}

func TestPerTokenLimiter_TracksHostsIndependently(t *testing.T) {
	l := NewPerTokenLimiter(0.001, 1)
	if !l.Allow("host-a") {
		t.Fatal("expected host-a's first request to be allowed")
	}
	if !l.Allow("host-b") {
		t.Fatal("expected host-b to have its own independent budget")
	}
}
