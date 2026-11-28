package hubapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/bitacora-dev/bitacora/internal/transport"
)

// DefaultEnrollmentCodeTTL is how long a one-time enrollment code stays
// redeemable. Fifteen minutes is long enough to walk to the other machine
// and paste it, and short enough that a code left in a chat window or a
// terminal scrollback is useless by the time anyone finds it.
const DefaultEnrollmentCodeTTL = 15 * time.Minute

// enrollmentCodeBytes is the entropy behind a code. 160 bits is far more
// than brute force needs to be hopeless, and the code is pasted rather than
// typed, so there is no usability argument for shortening it. ADR-0014's
// 32-bit QR pairing code is the counter-example: it is short because a
// person reads it off a screen, and it is correspondingly protected by a
// five-minute TTL and single use. An enrollment code grants permanent write
// capability for a host, so it does not get to make that trade.
const enrollmentCodeBytes = 20

// enrollRateLimit is the per-IP budget on POST /v1/enroll. The route is
// reachable without any credential except the code itself, so it is the one
// place in the hub where an unauthenticated stranger can make it do Argon2id
// work — one verify per stored live code, per request. Two bursts and then
// one attempt every ten seconds leaves a real enrollment (one request, maybe
// a retry) untouched while making an online guess of a 160-bit code
// arithmetically absurd rather than merely improbable.
const (
	enrollRatePerSecond = 0.1
	enrollBurst         = 2
	// enrollLimiterCapacity bounds the per-IP table. The key comes from the
	// peer address, which an attacker behind a /64 of IPv6 can vary at
	// will; without a cap that is a memory-growth primitive. On overflow the
	// table is dropped wholesale, which costs a forgotten budget, not an
	// unbounded map.
	enrollLimiterCapacity = 4096
)

// EnrollmentIssuer is the write side of the enrollment-code store that
// POST /v1/enrollments needs.
type EnrollmentIssuer interface {
	CreateEnrollmentCode(ctx context.Context, plaintextCode string, code transport.EnrollmentCode) error
	RedeemEnrollmentCode(ctx context.Context, plaintextCode string) (transport.EnrollmentCode, bool, error)
}

// CreateEnrollmentResponse is POST /v1/enrollments's body. Code is readable
// exactly once, here: the hub persists only its Argon2id hash and has no
// route that can hand it back.
type CreateEnrollmentResponse struct {
	Code      string    `json:"code"`
	Label     string    `json:"label,omitempty"`
	HostID    string    `json:"host_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	// EnrollCommand is the command to run on the new machine. It is built
	// here so the documented flow cannot drift from the route that serves
	// it, and it deliberately reads the code from stdin rather than from an
	// argument — see the note in docs/setup/alta-de-agentes.md.
	EnrollCommand string `json:"enroll_command"`
}

// EnrollResponse is POST /v1/enroll's body: the agent's identity and its
// ingest credential, the only time the plaintext token is readable.
type EnrollResponse struct {
	HostID     string    `json:"host_id"`
	Token      string    `json:"token"`
	CreatedAt  time.Time `json:"created_at"`
	HostIDPath string    `json:"host_id_path"`
	TokenPath  string    `json:"token_path"`
}

// handleCreateEnrollment implements POST /v1/enrollments: it mints a
// one-time code an operator carries to the new machine, so the plaintext
// ingest token never has to be copied by hand at all.
//
// Auth: a signed-in person with ADR-0023 `operate`, or the device token that
// POST /v1/hosts already accepts. Both are kept because they are the two
// ways an operator reaches this hub today, and dropping the device token
// would break the existing "Añadir servidor" clients.
func (s *Server) handleCreateEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.Enrollments == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent enrollment is not configured")
		return
	}

	var body struct {
		Name   string `json:"name"`
		HostID string `json:"host_id"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if body.HostID != "" {
		if err := validateHostID(body.HostID); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if !s.authorizeHostMutation(w, r, body.HostID) {
		return
	}

	code, err := newEnrollmentCode()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating enrollment code")
		return
	}
	expiresAt := time.Now().UTC().Add(DefaultEnrollmentCodeTTL)
	if err := s.Enrollments.CreateEnrollmentCode(r.Context(), code, transport.EnrollmentCode{
		Label:          body.Name,
		ExpectedHostID: body.HostID,
		ExpiresAt:      expiresAt,
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "storing enrollment code")
		return
	}

	writeJSON(w, http.StatusCreated, CreateEnrollmentResponse{
		Code:          code,
		Label:         body.Name,
		HostID:        body.HostID,
		ExpiresAt:     expiresAt,
		EnrollCommand: enrollCommand(r),
	})
}

// handleEnroll implements POST /v1/enroll: the new machine presents its
// one-time code and receives its host_id and ingest token.
//
// It is the only unauthenticated write route in the hub, which is why
// everything about it is narrow: a strict per-IP budget before any work is
// done, one generic error for every failure, and a code that is verified
// with the same constant-time Argon2id comparison as a token. The response
// says nothing a caller with an invalid code could use — not whether the
// code once existed, not whether it expired, not whether a host_id is known.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.Enrollments == nil || s.Hosts == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent enrollment is not configured")
		return
	}
	if !s.enrollLimiter().allow(r.RemoteAddr) {
		writeJSONError(w, http.StatusTooManyRequests, "too many enrollment attempts")
		return
	}

	var body struct {
		Code   string `json:"code"`
		HostID string `json:"host_id"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if body.Code == "" {
		// Phrased identically to a wrong code on purpose: "you sent no
		// code" and "your code is not valid" are the same answer here.
		writeJSONError(w, http.StatusUnauthorized, enrollRejection)
		return
	}
	if body.HostID != "" {
		if err := validateHostID(body.HostID); err != nil {
			writeJSONError(w, http.StatusUnauthorized, enrollRejection)
			return
		}
	}

	enrollment, ok, err := s.Enrollments.RedeemEnrollmentCode(r.Context(), body.Code)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "redeeming enrollment code")
		return
	}
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, enrollRejection)
		return
	}

	hostID, ok := resolveEnrolledHostID(enrollment, body.HostID)
	if !ok {
		// The code was pinned to a different host_id. The code is already
		// consumed — a code that survived being presented by the wrong
		// machine would be a code an attacker can test host_ids against.
		writeJSONError(w, http.StatusUnauthorized, enrollRejection)
		return
	}

	token, err := newIngestToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating ingest token")
		return
	}
	if err := s.addHostToken(r.Context(), hostID, enrollment.Label, token); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "registering ingest token")
		return
	}
	if s.HostRecords != nil {
		if err := s.HostRecords.CreateHost(r.Context(), hostID, enrollment.Label); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "storing host")
			return
		}
	}

	writeJSON(w, http.StatusCreated, EnrollResponse{
		HostID:     hostID,
		Token:      token,
		CreatedAt:  time.Now().UTC(),
		HostIDPath: agentHostIDPath,
		TokenPath:  agentTokenPath,
	})
}

// enrollRejection is the single answer every failed enrollment gets.
const enrollRejection = "invalid or expired enrollment code"

// resolveEnrolledHostID decides which host_id the enrolled machine gets.
//
// A code pinned to a host_id accepts only that one — an agent reinstall on a
// machine that already has an identity keeps it, and a machine presenting
// someone else's code cannot redirect the enrollment to an id of its own
// choosing. An unpinned code takes the agent's existing host_id when it has
// one, and mints a fresh ULID when it does not.
func resolveEnrolledHostID(enrollment transport.EnrollmentCode, presented string) (string, bool) {
	if enrollment.ExpectedHostID != "" {
		if presented != "" && presented != enrollment.ExpectedHostID {
			return "", false
		}
		return enrollment.ExpectedHostID, true
	}
	if presented != "" {
		return presented, true
	}
	generated, err := newHostID()
	if err != nil {
		return "", false
	}
	return generated, true
}

func newEnrollmentCode() (string, error) {
	buf := make([]byte, enrollmentCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating enrollment code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// enrollCommand renders the command for the new machine. The hub URL is
// taken from the request the operator's own browser made, so it is an
// address that demonstrably reaches this hub.
func enrollCommand(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	host := r.Host
	if host == "" {
		host = "HUB-ADDRESS"
	}
	return fmt.Sprintf("bitacora-agent enroll -hub %s://%s -code -", scheme, host)
}

// perIPLimiter is a bounded per-source-address rate limiter for the one
// unauthenticated write route. transport.PerTokenLimiter is deliberately not
// reused: it is keyed by host_id, which is a value the hub itself assigned
// and whose cardinality it controls, so it has no reason to bound its table.
type perIPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func (l *perIPLimiter) allow(remoteAddr string) bool {
	key := limiterKey(remoteAddr)

	l.mu.Lock()
	if l.limiters == nil {
		l.limiters = make(map[string]*rate.Limiter)
	}
	if len(l.limiters) >= enrollLimiterCapacity {
		l.limiters = make(map[string]*rate.Limiter)
	}
	limiter, ok := l.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(rate.Limit(enrollRatePerSecond), enrollBurst)
		l.limiters[key] = limiter
	}
	l.mu.Unlock()

	return limiter.Allow()
}

// limiterKey strips the ephemeral source port: budgeting per port would hand
// a single client one budget per connection, which is no budget at all.
func limiterKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (s *Server) enrollLimiter() *perIPLimiter {
	s.enrollLimiterOnce.Do(func() {
		s.enrollLimiterValue = &perIPLimiter{}
	})
	return s.enrollLimiterValue
}

// addHostToken persists a new credential, preferring the labelled form so
// `bita agent list` can show the operator-chosen name. A registrar that only
// implements the older unlabelled method still works — it just stores no
// label — which is what keeps the narrow fakes in the existing tests valid.
func (s *Server) addHostToken(ctx context.Context, hostID, label, token string) error {
	if admin, ok := s.Hosts.(transport.TokenAdmin); ok {
		_, err := admin.AddLabeledToken(ctx, hostID, label, token)
		return err
	}
	return s.Hosts.AddToken(hostID, token)
}

// decodeOptionalJSON decodes a body that is allowed to be absent. An empty
// body is the common case for "give me a brand new host", and
// http.Request.Body yields io.EOF for it, so that is not a malformed
// request.
func decodeOptionalJSON(r *http.Request, target any) error {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
