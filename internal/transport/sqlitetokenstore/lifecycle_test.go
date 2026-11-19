package sqlitetokenstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// fixedClock returns a store whose clock the test drives, so a rotation
// grace window and the last-seen throttle can be exercised without sleeping.
func newClockedStore(t *testing.T, at *time.Time) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("opening token store: %v", err)
	}
	store.now = func() time.Time { return *at }
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("closing token store: %v", err)
		}
	})
	return store
}

// The whole point of a revocation is that the agent stops being able to
// ingest. There is no cache on this path, so the next request must already
// be refused.
func TestRevokeHostTokensStopsTheNextLookup(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, ok, err := store.Lookup(ctx, "token-a"); err != nil || !ok {
		t.Fatalf("token should authenticate before revocation: ok=%v err=%v", ok, err)
	}

	revoked, err := store.RevokeHostTokens(ctx, "host-a")
	if err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked %d tokens, want 1", revoked)
	}

	hostID, ok, err := store.Lookup(ctx, "token-a")
	if err != nil {
		t.Fatalf("lookup after revocation: %v", err)
	}
	if ok {
		t.Fatalf("a revoked token still authenticated as %q", hostID)
	}
}

// Revoking twice must not be an error and must not rewrite the instant the
// credential actually stopped being valid.
func TestRevokeHostTokensIsIdempotentAndKeepsTheFirstInstant(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, err := store.RevokeHostTokens(ctx, "host-a"); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	first := revocationInstant(t, store, "host-a")

	now = now.Add(time.Hour)
	revoked, err := store.RevokeHostTokens(ctx, "host-a")
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("second revoke reported %d tokens, want 0", revoked)
	}
	if again := revocationInstant(t, store, "host-a"); !again.Equal(first) {
		t.Fatalf("revocation instant moved from %s to %s", first, again)
	}
}

func revocationInstant(t *testing.T, store *Store, hostID string) time.Time {
	t.Helper()
	tokens, err := store.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing tokens: %v", err)
	}
	for _, token := range tokens {
		if token.HostID == hostID {
			return token.RevokedAt
		}
	}
	t.Fatalf("no token found for host %q", hostID)
	return time.Time{}
}

// Rotation with a grace period is the whole reason it is not just "revoke
// then create": the agent currently running must keep ingesting until the
// operator has installed the new token.
func TestRotateHostTokensKeepsTheOldTokenForTheGracePeriod(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}

	rotated, err := store.RotateHostTokens(ctx, "host-a", "", "new-token", 15*time.Minute)
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}
	if rotated.Label != "vps" {
		t.Fatalf("rotated token label = %q, want the inherited \"vps\"", rotated.Label)
	}
	if rotated.RotatedFrom == 0 {
		t.Fatal("rotated token does not record which credential it replaced")
	}

	for _, token := range []string{"old-token", "new-token"} {
		if _, ok, err := store.Lookup(ctx, token); err != nil || !ok {
			t.Fatalf("%s should still authenticate inside the grace window: ok=%v err=%v", token, ok, err)
		}
	}

	now = now.Add(16 * time.Minute)
	if _, ok, err := store.Lookup(ctx, "old-token"); err != nil || ok {
		t.Fatalf("old token outlived its grace window: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.Lookup(ctx, "new-token"); err != nil || !ok {
		t.Fatalf("new token stopped working: ok=%v err=%v", ok, err)
	}
}

// grace=0 is the "I believe this token is compromised" rotation: the old one
// must be dead before the call returns, not in fifteen minutes.
func TestRotateHostTokensWithoutGraceRevokesImmediately(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, err := store.RotateHostTokens(ctx, "host-a", "", "new-token", 0); err != nil {
		t.Fatalf("rotating: %v", err)
	}

	if _, ok, _ := store.Lookup(ctx, "old-token"); ok {
		t.Fatal("old token still authenticates after a rotation without grace")
	}
	if _, ok, err := store.Lookup(ctx, "new-token"); err != nil || !ok {
		t.Fatalf("new token does not authenticate: ok=%v err=%v", ok, err)
	}
}

func TestRotateHostTokensRefusesAHostWithNoCredential(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.RotateHostTokens(context.Background(), "host-unknown", "", "new-token", 0); err == nil {
		t.Fatal("rotating a host with no token should fail loudly, not mint a first credential")
	}
}

// A rotation that fails must not leave the host with two live credentials or
// none at all. An empty plaintext is the cheapest way to fail the insert
// after the retirement UPDATE has already run inside the transaction.
func TestRotateHostTokensRollsBackTheRetirementWhenTheInsertFails(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, err := store.RotateHostTokens(ctx, "host-a", "", "", time.Minute); err == nil {
		t.Fatal("rotating to an empty token should fail")
	}

	if _, ok, err := store.Lookup(ctx, "old-token"); err != nil || !ok {
		t.Fatalf("a failed rotation retired the only working credential: ok=%v err=%v", ok, err)
	}
}

func TestListTokensReportsLifecycleWithoutTokenMaterial(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, ok, err := store.Lookup(ctx, "token-a"); err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}

	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("listed %d tokens, want 1", len(tokens))
	}
	token := tokens[0]
	if token.HostID != "host-a" || token.Label != "vps" {
		t.Fatalf("token = %+v, want host-a/vps", token)
	}
	if !token.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %s, want %s", token.CreatedAt, now)
	}
	if !token.LastSeenAt.Equal(now) {
		t.Fatalf("last_seen_at = %s, want %s after a successful lookup", token.LastSeenAt, now)
	}
	if !token.Active(now) || token.Revoked() {
		t.Fatalf("a fresh token reported itself inactive: %+v", token)
	}
}

// A listing is an operator surface. It must never be able to leak the hash,
// so the struct it returns has nowhere to put one.
func TestListTokensNeverExposesAHash(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, field := range []string{tokens[0].HostID, tokens[0].Label} {
		if field == "token-a" {
			t.Fatal("the plaintext token appeared in a listing")
		}
	}
}

func TestTouchLastSeenThrottlesRepeatedWrites(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()

	created, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a")
	if err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if err := store.TouchLastSeen(ctx, created.ID, now); err != nil {
		t.Fatalf("first touch: %v", err)
	}

	later := now.Add(10 * time.Second)
	if err := store.TouchLastSeen(ctx, created.ID, later); err != nil {
		t.Fatalf("throttled touch returned an error: %v", err)
	}
	if got := lastSeenOf(t, store, created.ID); !got.Equal(now) {
		t.Fatalf("last_seen_at = %s, want the throttled %s", got, now)
	}

	muchLater := now.Add(lastSeenWriteInterval + time.Second)
	if err := store.TouchLastSeen(ctx, created.ID, muchLater); err != nil {
		t.Fatalf("touch after the interval: %v", err)
	}
	if got := lastSeenOf(t, store, created.ID); !got.Equal(muchLater) {
		t.Fatalf("last_seen_at = %s, want %s once the interval elapsed", got, muchLater)
	}
}

func lastSeenOf(t *testing.T, store *Store, id int64) time.Time {
	t.Helper()
	tokens, err := store.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing tokens: %v", err)
	}
	for _, token := range tokens {
		if token.ID == id {
			return token.LastSeenAt
		}
	}
	t.Fatalf("no token with id %d", id)
	return time.Time{}
}

// The migration has to run against a tokens.db written by the build that
// only had the original three columns, and the tokens in it have to keep
// working. This is the upgrade every existing installation performs.
func TestNewMigratesALegacyTokensDatabaseWithoutBreakingItsTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening legacy database: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE ingest_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		host_id    TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("creating legacy table: %v", err)
	}
	// Written the way the old build wrote it: a real Argon2id hash, and no
	// lifecycle columns at all.
	store, err := New(path)
	if err != nil {
		t.Fatalf("migrating legacy store: %v", err)
	}
	if err := store.AddToken("legacy-host", "legacy-token"); err != nil {
		t.Fatalf("adding token to the migrated store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing migrated store: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("closing legacy handle: %v", err)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopening migrated store: %v", err)
	}
	defer reopened.Close()

	hostID, ok, err := reopened.Lookup(context.Background(), "legacy-token")
	if err != nil || !ok || hostID != "legacy-host" {
		t.Fatalf("legacy token stopped working: hostID=%q ok=%v err=%v", hostID, ok, err)
	}
	tokens, err := reopened.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing migrated tokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Revoked() {
		t.Fatalf("migrated tokens = %+v, want one active credential", tokens)
	}
}

// Opening the same file twice is what happens on every hub restart, and
// `bita agent` opens it alongside the running hub.
func TestNewIsRerunnableOverItsOwnDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	for attempt := range 3 {
		store, err := New(path)
		if err != nil {
			t.Fatalf("attempt %d: opening store: %v", attempt, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("attempt %d: closing store: %v", attempt, err)
		}
	}
}

// ADR-0008 fixes 0600 for token material on the agent; the hub's copy of
// every hash in the installation has no business being world-readable.
func TestNewRestrictsTheDatabaseFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating database: %v", err)
	}
	if perm := info.Mode().Perm(); perm&^0o600 != 0 {
		t.Fatalf("tokens.db mode = %04o, want no bits outside 0600", perm)
	}
}

// stubReceiver accepts every batch, so the test asserts the auth decision
// and nothing else.
type stubReceiver struct{ batches int }

func (s *stubReceiver) ReceiveBatch(context.Context, string, *bitacorapb.Batch) error {
	s.batches++
	return nil
}

// The acceptance criterion is about the ingest endpoint, not about the
// store's own query: a revoked credential has to be refused by the real
// POST /v1/ingest handler, over the real store, with the Argon2id
// verification and the per-host rate limiter still in place.
func TestIngestRejectsARevokedTokenThroughTheRealHandler(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "token-a"); err != nil {
		t.Fatalf("adding token: %v", err)
	}

	receiver := &stubReceiver{}
	srv := &transport.Server{
		Tokens:      store,
		Idempotency: transport.NewMemoryIdempotencyStore(),
		Receiver:    receiver,
		Limiter:     transport.NewPerTokenLimiter(100, 100),
	}
	handler := srv.Handler()

	if status := ingest(t, handler, "token-a", "host-a", "batch-1"); status != http.StatusOK {
		t.Fatalf("status before revocation = %d, want %d", status, http.StatusOK)
	}
	if receiver.batches != 1 {
		t.Fatalf("receiver saw %d batches, want 1", receiver.batches)
	}

	if _, err := store.RevokeHostTokens(ctx, "host-a"); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	// No sleep, no restart, no cache to wait out: the next request is the
	// one that has to be refused.
	if status := ingest(t, handler, "token-a", "host-a", "batch-2"); status != http.StatusUnauthorized {
		t.Fatalf("status after revocation = %d, want %d", status, http.StatusUnauthorized)
	}
	if receiver.batches != 1 {
		t.Fatalf("receiver saw %d batches, want the revoked one to never arrive", receiver.batches)
	}
}

// A rotated credential has to keep ingesting for its grace window, and the
// replacement has to work from the moment it exists.
func TestIngestAcceptsBothCredentialsDuringARotationGrace(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	store := newClockedStore(t, &now)
	ctx := context.Background()
	if _, err := store.AddLabeledToken(ctx, "host-a", "vps", "old-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	if _, err := store.RotateHostTokens(ctx, "host-a", "", "new-token", 15*time.Minute); err != nil {
		t.Fatalf("rotating: %v", err)
	}

	srv := &transport.Server{
		Tokens:      store,
		Idempotency: transport.NewMemoryIdempotencyStore(),
		Receiver:    &stubReceiver{},
	}
	handler := srv.Handler()

	for i, token := range []string{"old-token", "new-token"} {
		if status := ingest(t, handler, token, "host-a", fmt.Sprintf("batch-%d", i)); status != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d inside the grace window", token, status, http.StatusOK)
		}
	}

	now = now.Add(16 * time.Minute)
	if status := ingest(t, handler, "old-token", "host-a", "batch-late"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d once the grace window closed", status, http.StatusUnauthorized)
	}
}

// ingest posts one minimal batch the way the agent does: zstd-compressed
// protobuf with a bearer token.
func ingest(t *testing.T, handler http.Handler, token, hostID, batchID string) int {
	t.Helper()
	raw, err := proto.Marshal(&bitacorapb.Batch{HostId: hostID, BatchId: batchID})
	if err != nil {
		t.Fatalf("marshalling batch: %v", err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("creating zstd writer: %v", err)
	}
	body := encoder.EncodeAll(raw, nil)
	encoder.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}
