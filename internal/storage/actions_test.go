package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
	_ "modernc.org/sqlite"
)

func TestSQLiteActionBackendPersistsHashedSingleUseTokensAndAppendOnlyAudit(t *testing.T) {
	dir := t.TempDir()
	first, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	service := actionconfirm.NewStore(first)
	issued, err := service.Issue(context.Background(), actionconfirm.IssueInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	service = actionconfirm.NewStore(reopened)
	if err := service.Confirm(context.Background(), actionconfirm.ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token,
	}); err != nil {
		t.Fatalf("confirming persisted token: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	afterConsumeRestart, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = afterConsumeRestart.Close() })
	service = actionconfirm.NewStore(afterConsumeRestart)
	if err := service.Confirm(context.Background(), actionconfirm.ConfirmInput{
		Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE,
		RequestID: issued.RequestID, Token: issued.Token,
	}); !errors.Is(err, actionconfirm.ErrTokenUsed) {
		t.Fatalf("reused token after restart error = %v, want ErrTokenUsed", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "hosts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash string
	if err := db.QueryRow(`SELECT token_hash FROM action_tokens WHERE request_id = ?`, issued.RequestID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == issued.Token {
		t.Fatal("action token was stored as plaintext")
	}
	if _, err := db.Exec(`UPDATE action_audit_events SET result = 'tampered'`); err == nil {
		t.Fatal("append-only action audit accepted an update")
	}
	if _, err := db.Exec(`DELETE FROM action_audit_events`); err == nil {
		t.Fatal("append-only action audit accepted a delete")
	}
}

func TestSQLiteActionBackendDoesNotReturnExpiredOrders(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := actionconfirm.NewStore(store)
	issued, err := service.Issue(context.Background(), actionconfirm.IssueInput{Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Confirm(context.Background(), actionconfirm.ConfirmInput{Subject: "human-a", HostID: "host-a", Operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, RequestID: issued.RequestID, Token: issued.Token}); err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().UTC().Add(actionconfirm.DefaultTokenTTL + time.Second)
	if order := store.NextPendingOrder(context.Background(), "host-a", expiredAt); order != nil {
		t.Fatalf("expired order was delivered: %+v", order)
	}
}
