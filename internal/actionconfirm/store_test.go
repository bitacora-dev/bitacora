package actionconfirm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

func TestStoreConfirmsOnlyTheBoundHumanRequestOnce(t *testing.T) {
	store := NewStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }

	issued, err := store.Issue(context.Background(), IssueInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, NetworkOrigin: "192.0.2.1:443",
	})
	if err != nil {
		t.Fatalf("issuing action token: %v", err)
	}
	if issued.Token == "" || issued.RequestID == "" {
		t.Fatalf("expected a token and request ID, got %+v", issued)
	}
	for _, attempt := range []ConfirmInput{
		{Subject: "human-b", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, RequestID: issued.RequestID, Token: issued.Token},
		{Subject: "human-a", HostID: "host-b", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, RequestID: issued.RequestID, Token: issued.Token},
		{Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES, RequestID: issued.RequestID, Token: issued.Token},
		{Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, RequestID: "another-request", Token: issued.Token},
	} {
		if err := store.Confirm(context.Background(), attempt); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("wrong token binding error = %v, want ErrInvalidToken", err)
		}
	}
	if err := store.Confirm(context.Background(), ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token, NetworkOrigin: "192.0.2.1:443",
	}); err != nil {
		t.Fatalf("confirming action: %v", err)
	}
	if err := store.Confirm(context.Background(), ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token, NetworkOrigin: "192.0.2.1:443",
	}); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("second confirmation error = %v, want ErrTokenUsed", err)
	}

	order := store.NextPendingOrder(context.Background(), "host-a")
	if order == nil || order.GetRequestId() != issued.RequestID || order.GetOperation() != bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE {
		t.Fatalf("unexpected pending order: %+v", order)
	}
	if other := store.NextPendingOrder(context.Background(), "host-b"); other != nil {
		t.Fatalf("token bound to host-a leaked to host-b: %+v", other)
	}
}

func TestStoreRejectsExpiredAndWrongBoundTokens(t *testing.T) {
	store := NewStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	issued, err := store.Issue(context.Background(), IssueInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Confirm(context.Background(), ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES,
		RequestID: issued.RequestID, Token: issued.Token,
	}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong operation error = %v, want ErrInvalidToken", err)
	}
	now = now.Add(DefaultTokenTTL + time.Second)
	if err := store.Confirm(context.Background(), ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token,
	}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired confirmation error = %v, want ErrTokenExpired", err)
	}
}

func TestStoreAppendsAuditBeforeOrderAndRecordsDeliveryFailure(t *testing.T) {
	backend := newMemoryBackend()
	backend.queueErr = errors.New("offline")
	store := NewStore(backend)
	issued, err := store.Issue(context.Background(), IssueInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Confirm(context.Background(), ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token,
	}); err == nil {
		t.Fatal("expected queue failure")
	}
	events := backend.AuditEvents()
	if len(events) != 2 || events[0].Result != AuditResultConfirmed || events[1].Result != AuditResultDeliveryFailed {
		t.Fatalf("want append-only confirmed then delivery_failed audit events, got %+v", events)
	}
	if len(backend.operations) < 3 || backend.operations[len(backend.operations)-3] != "audit:confirmed" || backend.operations[len(backend.operations)-2] != "queue" || backend.operations[len(backend.operations)-1] != "audit:delivery_failed" {
		t.Fatalf("audit was not recorded before delivery attempt: %v", backend.operations)
	}
}
