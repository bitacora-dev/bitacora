package agentactions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

func TestEmptyAllowlistLeavesChannelInert(t *testing.T) {
	manager := NewManager(Allowlist{}, nil)
	manager.now = func() time.Time { return time.Unix(100, 0) }

	decision := manager.Handle(&bitacorapb.PendingPackageOperation{
		Operation:   bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestId:   "request-1",
		ExpiresAtMs: time.Unix(101, 0).UnixMilli(),
	})
	if decision != DecisionRejected {
		t.Fatalf("expected disabled default to reject, got %q", decision)
	}
	if got := manager.PollInterval(30 * time.Second); got != 30*time.Second {
		t.Fatalf("disabled agent changed cadence to %s", got)
	}
}

func TestManagerRejectsUnlistedOperation(t *testing.T) {
	manager := NewManager(Allowlist{RefreshPackageCache: true}, nil)
	manager.now = func() time.Time { return time.Unix(100, 0) }

	decision := manager.Handle(&bitacorapb.PendingPackageOperation{
		Operation:   bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES,
		RequestId:   "request-1",
		ExpiresAtMs: time.Unix(101, 0).UnixMilli(),
	})
	if decision != DecisionRejected {
		t.Fatalf("expected operation outside local allowlist to be rejected, got %q", decision)
	}
}

func TestManagerConsumesRequestOnlyOnceAndRejectsExpired(t *testing.T) {
	manager := NewManager(Allowlist{RefreshPackageCache: true}, nil)
	manager.now = func() time.Time { return time.Unix(100, 0) }
	order := &bitacorapb.PendingPackageOperation{
		Operation:   bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestId:   "request-1",
		ExpiresAtMs: time.Unix(101, 0).UnixMilli(),
	}
	if got := manager.Handle(order); got != DecisionAccepted {
		t.Fatalf("expected first order to be accepted, got %q", got)
	}
	if got := manager.Handle(order); got != DecisionAlreadyConsumed {
		t.Fatalf("expected duplicate order to be rejected, got %q", got)
	}
	manager.Resolve(order.GetRequestId())
	if got := manager.Handle(&bitacorapb.PendingPackageOperation{
		Operation:   bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestId:   "expired",
		ExpiresAtMs: time.Unix(99, 0).UnixMilli(),
	}); got != DecisionExpired {
		t.Fatalf("expected expired order to be rejected, got %q", got)
	}
}

func TestManagerCadenceOnlyAcceleratesForEnabledPendingOrder(t *testing.T) {
	const normal = 30 * time.Second
	manager := NewManager(Allowlist{RefreshPackageCache: true}, nil)
	manager.now = func() time.Time { return time.Unix(100, 0) }
	if got := manager.PollInterval(normal); got != normal {
		t.Fatalf("enabled agent without an order changed cadence to %s", got)
	}
	order := &bitacorapb.PendingPackageOperation{
		Operation:   bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestId:   "pending",
		ExpiresAtMs: time.Unix(101, 0).UnixMilli(),
	}
	if got := manager.Handle(order); got != DecisionAccepted {
		t.Fatalf("expected pending order accepted, got %q", got)
	}
	if got := manager.PollInterval(normal); got != MaxPendingPollInterval {
		t.Fatalf("expected pending order cadence of %s, got %s", MaxPendingPollInterval, got)
	}
	manager.Resolve(order.GetRequestId())
	if got := manager.PollInterval(normal); got != normal {
		t.Fatalf("resolved order did not restore cadence, got %s", got)
	}
}

func TestLoadAllowlistDefaultsToEmptyAndRejectsUnknownFields(t *testing.T) {
	allowlist, err := LoadAllowlist(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || allowlist.Enabled() {
		t.Fatalf("missing configuration must be disabled by default, got %+v, %v", allowlist, err)
	}
	path := filepath.Join(t.TempDir(), "actions.json")
	if err := os.WriteFile(path, []byte(`{"refresh_package_cache":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowlist, err = LoadAllowlist(path)
	if err != nil || !allowlist.RefreshPackageCache || allowlist.ApplyPendingPackageUpdates {
		t.Fatalf("unexpected local allowlist %+v, %v", allowlist, err)
	}
	if err := os.WriteFile(path, []byte(`{"hub_can_enable_anything":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAllowlist(path); err == nil {
		t.Fatal("expected unknown configuration field to be rejected")
	}
}
