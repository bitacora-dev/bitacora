package sqlitetokenstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

func TestRedeemEnrollmentCodeReturnsTheIntentItWasCreatedWith(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	want := transport.EnrollmentCode{
		Label:          "vps-berlin",
		ExpectedHostID: "01JABCDEF",
		ExpiresAt:      now.Add(15 * time.Minute),
	}
	if err := store.CreateEnrollmentCode(ctx, "plain-code", want); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	got, ok, err := store.RedeemEnrollmentCode(ctx, "plain-code")
	if err != nil {
		t.Fatalf("redeeming: %v", err)
	}
	if !ok {
		t.Fatal("a valid code was refused")
	}
	if got.Label != want.Label || got.ExpectedHostID != want.ExpectedHostID {
		t.Fatalf("redeemed %+v, want label/host of %+v", got, want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", got.ExpiresAt, want.ExpiresAt)
	}
}

// Single use is the property that makes a leaked code worth little: whoever
// gets there second gets nothing.
func TestRedeemEnrollmentCodeConsumesTheCode(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.CreateEnrollmentCode(ctx, "plain-code", transport.EnrollmentCode{
		Label: "vps", ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatalf("creating code: %v", err)
	}
	if _, ok, err := store.RedeemEnrollmentCode(ctx, "plain-code"); err != nil || !ok {
		t.Fatalf("first redemption: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.RedeemEnrollmentCode(ctx, "plain-code"); err != nil || ok {
		t.Fatalf("second redemption succeeded: ok=%v err=%v", ok, err)
	}
}

// Two agents (or an agent and an attacker who copied the code) redeeming at
// once must not both receive a credential.
func TestRedeemEnrollmentCodeIsSingleUseUnderConcurrency(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.CreateEnrollmentCode(ctx, "plain-code", transport.EnrollmentCode{
		Label: "vps", ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	const racers = 4
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
	)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := store.RedeemEnrollmentCode(ctx, "plain-code")
			if err != nil {
				return
			}
			if ok {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("%d of %d concurrent redemptions succeeded, want exactly 1", successes, racers)
	}
}

func TestRedeemEnrollmentCodeRefusesAnExpiredCode(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	if err := store.CreateEnrollmentCode(ctx, "plain-code", transport.EnrollmentCode{
		Label: "vps", ExpiresAt: now.Add(15 * time.Minute),
	}); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	now = now.Add(16 * time.Minute)
	if _, ok, err := store.RedeemEnrollmentCode(ctx, "plain-code"); err != nil || ok {
		t.Fatalf("an expired code was redeemed: ok=%v err=%v", ok, err)
	}
}

// An unknown code, an expired one and a consumed one are the same answer, so
// probing cannot tell them apart.
func TestRedeemEnrollmentCodeRefusesAnUnknownCodeWithoutDetail(t *testing.T) {
	store, _ := newTestStore(t)
	code, ok, err := store.RedeemEnrollmentCode(context.Background(), "never-created")
	if err != nil {
		t.Fatalf("redeeming an unknown code reported an error: %v", err)
	}
	if ok || code != (transport.EnrollmentCode{}) {
		t.Fatalf("unknown code returned %+v ok=%v", code, ok)
	}
}

func TestCreateEnrollmentCodeRequiresAnExpiry(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.CreateEnrollmentCode(context.Background(), "plain-code", transport.EnrollmentCode{Label: "vps"}); err == nil {
		t.Fatal("a code with no expiry was accepted")
	}
}

func TestPendingEnrollmentCodesCountsOnlyRedeemableOnes(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	for _, code := range []struct {
		plaintext string
		expires   time.Time
	}{
		{"live", now.Add(15 * time.Minute)},
		{"stale", now.Add(time.Minute)},
		{"used", now.Add(15 * time.Minute)},
	} {
		if err := store.CreateEnrollmentCode(ctx, code.plaintext, transport.EnrollmentCode{Label: "vps", ExpiresAt: code.expires}); err != nil {
			t.Fatalf("creating %s: %v", code.plaintext, err)
		}
	}
	if _, ok, err := store.RedeemEnrollmentCode(ctx, "used"); err != nil || !ok {
		t.Fatalf("redeeming: ok=%v err=%v", ok, err)
	}

	now = now.Add(2 * time.Minute)
	pending, err := store.PendingEnrollmentCodes(ctx)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1 (one live, one expired, one consumed)", pending)
	}
}
