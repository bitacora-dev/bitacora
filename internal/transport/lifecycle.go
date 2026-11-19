package transport

import (
	"context"
	"errors"
	"time"
)

// ErrNoCredential reports that a host has no ingest credential to act on.
// Rotation replaces a credential; it never mints a first one for a host
// nobody enrolled, and a caller has to be able to tell that apart from the
// store having failed.
var ErrNoCredential = errors.New("host has no ingest credential")

// TokenInfo is the lifecycle metadata of one ingest credential (ADR-0008:
// "los tokens se pueden revocar y rotar sin reinstalar el agente"). It
// deliberately carries no token material at all — not the plaintext, which
// exists only in the instant it is minted, and not the Argon2id hash, which
// no caller outside the store has any use for. A listing is an operator
// surface, so anything it carries can end up on a terminal or in a UI.
type TokenInfo struct {
	// ID is the store's own row identity. It is what rotation records as a
	// predecessor, and what TouchLastSeen updates.
	ID int64
	// HostID is the host this credential is bound to. The ingest path
	// rejects a batch whose host_id does not match it.
	HostID string
	// Label is the operator-chosen name (`bita agent create <name>`). It is
	// how a person refers to an agent; host_id is a ULID nobody remembers.
	Label string
	// CreatedAt is when the credential was minted.
	CreatedAt time.Time
	// LastSeenAt is the last time this credential authenticated a request.
	// Zero means it never has, which is how an enrollment that was issued
	// but never completed stays visible.
	LastSeenAt time.Time
	// RevokedAt is non-zero once an operator revoked the credential. A
	// revoked row is kept rather than deleted: the audit answer to "was
	// this token ever valid, and until when" has to survive the revocation.
	RevokedAt time.Time
	// ExpiresAt is non-zero only for a credential rotated out with a grace
	// period: it keeps working until this instant and then stops on its own.
	ExpiresAt time.Time
	// RotatedFrom is the ID of the credential this one replaced, or 0 when
	// it was not born of a rotation.
	RotatedFrom int64
}

// Revoked reports whether an operator took this credential out of service.
func (t TokenInfo) Revoked() bool {
	return !t.RevokedAt.IsZero()
}

// Expired reports whether a rotation grace period has run out.
func (t TokenInfo) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Active reports whether the ingest path would accept this credential now.
func (t TokenInfo) Active(now time.Time) bool {
	return !t.Revoked() && !t.Expired(now)
}

// TokenAdmin is the administrative side of the ingest token store: the
// operations `bita agent` and the hub's host-mutation routes need, separate
// from the TokenStore interface the ingest hot path uses.
//
// It is an interface so hubapi depends on the lifecycle contract rather than
// on sqlitetokenstore, exactly as it already does for TokenStore.
type TokenAdmin interface {
	// AddLabeledToken mints nothing: it persists the Argon2id hash of an
	// already-generated plaintext token, bound to hostID and named label.
	AddLabeledToken(ctx context.Context, hostID, label, plaintextToken string) (TokenInfo, error)
	// ListTokens returns every credential ever issued, revoked ones
	// included, oldest first.
	ListTokens(ctx context.Context) ([]TokenInfo, error)
	// RevokeHostTokens revokes every active credential bound to hostID and
	// reports how many it revoked. Revoking an already-revoked host is not
	// an error — it reports 0, so a retried revocation is safe.
	RevokeHostTokens(ctx context.Context, hostID string) (int, error)
	// RotateHostTokens binds plaintextToken to hostID as a replacement and
	// retires the credentials it replaces. grace is how long the retired
	// ones keep working; zero revokes them immediately.
	RotateHostTokens(ctx context.Context, hostID, label, plaintextToken string, grace time.Duration) (TokenInfo, error)
}

// EnrollmentCode is what a redeemed one-time enrollment code authorizes. It
// carries no code material either: the code is verified, consumed and then
// only its intent survives.
type EnrollmentCode struct {
	// Label is the name the operator gave the machine being enrolled. It
	// becomes the new credential's label.
	Label string
	// ExpectedHostID, when set, pins the code to one host_id. A machine
	// that already has a host_id (a reinstall of the agent, not of the OS)
	// is enrolled with it rather than being given a second identity.
	ExpectedHostID string
	// ExpiresAt is the instant the code stopped being redeemable. It is
	// returned for the audit trail, not as something to re-check: the store
	// has already refused an expired code by the time this exists.
	ExpiresAt time.Time
}

// EnrollmentStore persists one-time enrollment codes. Like tokens, a code is
// stored only as its Argon2id hash: the hub cannot show a code again, and a
// stolen database yields no redeemable code.
type EnrollmentStore interface {
	// CreateEnrollmentCode persists the hash of plaintextCode with the
	// intent it authorizes and the instant it stops being redeemable.
	CreateEnrollmentCode(ctx context.Context, plaintextCode string, code EnrollmentCode) error
	// RedeemEnrollmentCode verifies plaintextCode and consumes it
	// atomically. A code that is unknown, expired or already consumed all
	// report ok=false with no error and no distinguishing detail: the
	// caller must not be able to tell those three apart, or probing codes
	// becomes an oracle for which ones exist.
	RedeemEnrollmentCode(ctx context.Context, plaintextCode string) (EnrollmentCode, bool, error)
}
