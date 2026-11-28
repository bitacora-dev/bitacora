package hubapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/oklog/ulid/v2"
)

// hostIDMaxLen bounds an operator-supplied host_id. A generated one is a
// 26-character ULID (schema.LoadOrCreateHostID); the extra room is for
// hosts enrolled with an id that came from somewhere else.
const hostIDMaxLen = 64

// ingestTokenBytes is the entropy behind a generated ingest token, same
// size as the device tokens minted in devicetoken.go.
const ingestTokenBytes = 32

// DefaultRotationGrace is how long a rotated-out credential keeps working.
// Rotation exists so an operator does not have to reinstall the agent
// (ADR-0008), which means there is a window between minting the replacement
// and installing it on the machine — and during that window the agent must
// keep ingesting, or rotation would cost exactly the data loss it is
// supposed to avoid. Fifteen minutes covers a manual copy without leaving
// a superseded credential alive for an afternoon.
//
// It is a window, not a weakening: a caller that believes a token is
// compromised asks for grace_seconds=0 and the old credential is dead before
// the response is written.
const DefaultRotationGrace = 15 * time.Minute

// HostRegistrar is the write side of the ingest token store that
// POST /v1/hosts needs — narrowed to one method so hubapi doesn't depend
// on sqlitetokenstore (a fake is enough in tests). The real
// implementation is *sqlitetokenstore.Store, the same one
// `bitacora-hub -add-token` writes through.
type HostRegistrar interface {
	AddToken(hostID, plaintextToken string) error
}

// HostRecordStore persists metadata that is safe to expose to paired clients.
// It intentionally contains no ingest token material.
type HostRecordStore interface {
	CreateHost(ctx context.Context, hostID, name string) error
	ListHosts(ctx context.Context) ([]schema.Host, error)
}

// CreateHostResponse is POST /v1/hosts's response. Token is the only time
// the plaintext ingest token is ever readable: the hub persists nothing
// but its Argon2id hash (ADR-0008), exactly like the -add-token CLI path.
type CreateHostResponse struct {
	HostID    string    `json:"host_id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	// HostIDPath and TokenPath are where the agent expects each value on
	// the new machine (ADR-0004 and ADR-0008 respectively). They're in the
	// response so the UI renders the install command from the hub's own
	// contract instead of hardcoding paths that would silently drift.
	HostIDPath string `json:"host_id_path"`
	TokenPath  string `json:"token_path"`
}

// agentHostIDPath and agentTokenPath mirror schema.DefaultHostIDPath and
// ADR-0008's token file location. They're duplicated as constants rather
// than imported so hubapi (a hub-side package) doesn't pull in the
// agent's defaults; the values are part of the enrollment contract the
// UI shows, not internal wiring.
const (
	agentHostIDPath = "/var/lib/bitacora/host_id"
	agentTokenPath  = "/etc/bitacora/token"
)

// handleCreateHost implements POST /v1/hosts: it mints a host_id (or takes
// the one the caller already has) plus a fresh ingest token, registers the
// token's hash against that host_id, and returns the plaintext token once.
//
// Auth: authorizeHostMutation, the same guard the revoke and rotate routes
// use — a signed-in person with ADR-0023 `operate`, or the device token
// (ADR-0014) this route has always required. The device-token path is
// unchanged for every existing client; what the human path adds is that a
// verified session with operate no longer has to also present a device
// token to enroll a server, which is the same reasoning requireDeviceToken
// already applies to the read routes.
//
// What has not changed is that *something* is always required, including
// when no device has ever been paired. handleDevicePair deliberately lets
// the very first pairing through unauthenticated because there's no other
// way to bootstrap a reader; that exception must not extend here.
// Enrolling a host grants permanent *write* capability into the hub's
// storage, so an unauthenticated caller reaching this route could inject
// arbitrary metrics, events and logs for a host of their choosing. Read
// bootstrapping and write provisioning are not the same risk.
func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Hosts == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "host enrollment is not configured")
		return
	}

	var body struct {
		HostID string `json:"host_id"`
		Name   string `json:"name"`
	}
	// An empty body is the common case ("give me a brand new host"), and
	// http.Request.Body yields io.EOF for it, so that isn't an error.
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	hostID := body.HostID
	if hostID != "" {
		if err := validateHostID(hostID); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if !s.authorizeHostMutation(w, r, hostID) {
		return
	}
	if hostID == "" {
		generated, err := newHostID()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "generating host id")
			return
		}
		hostID = generated
	}

	ingestToken, err := newIngestToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating ingest token")
		return
	}

	if err := s.addHostToken(r.Context(), hostID, body.Name, ingestToken); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "registering ingest token")
		return
	}
	if s.HostRecords != nil {
		if err := s.HostRecords.CreateHost(r.Context(), hostID, body.Name); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "storing host")
			return
		}
	}

	writeJSON(w, http.StatusCreated, CreateHostResponse{
		HostID:     hostID,
		Token:      ingestToken,
		CreatedAt:  time.Now().UTC(),
		HostIDPath: agentHostIDPath,
		TokenPath:  agentTokenPath,
	})
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateHost(w, r)
	case http.MethodGet:
		s.requireDeviceToken(s.handleListHosts)(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HostListEntry is one row of GET /v1/hosts: the host record plus the
// lifecycle of its ingest credential (ADR-0008). The embedded schema.Host
// inlines the fields existing clients already read, so the credential
// columns are purely additive — the web UI's Host type keeps parsing this
// response unchanged.
//
// It carries no token material. Status is derived rather than stored so that
// "revoked" and "the ingest path refuses it" cannot drift apart.
type HostListEntry struct {
	schema.Host
	// Status is one of HostStatus*. It is the single field a UI needs to
	// render the row, so each client does not re-derive it from the flags.
	Status string `json:"status"`
	// CreatedAt is when the host's credential was issued. Absent for a host
	// record with no credential at all.
	CreatedAt *time.Time `json:"created_at,omitempty"`
	// TokenLastSeenAt is the last time the credential authenticated a
	// request. It differs from LastSeenAt, which the agent's manifest
	// updates: a host whose token still authenticates while its manifest
	// goes stale is a different fault from one that stopped connecting.
	TokenLastSeenAt *time.Time `json:"token_last_seen_at,omitempty"`
	// Revoked is the flat form of Status for a caller that only branches on
	// "can this agent still send data".
	Revoked bool `json:"revoked"`
}

// The statuses GET /v1/hosts reports for an enrolled host.
const (
	// HostStatusActive: a credential exists and the ingest path accepts it.
	HostStatusActive = "active"
	// HostStatusRevoked: an operator took the credential out of service.
	HostStatusRevoked = "revoked"
	// HostStatusExpired: a rotation grace window ran out without the new
	// token being installed, so the host is silent and the reason is known.
	HostStatusExpired = "expired"
	// HostStatusNoCredential: a host record with no ingest credential —
	// either enrolled before tokens were tracked, or revoked and purged.
	HostStatusNoCredential = "no_credential"
)

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	if s.HostRecords == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "host listing is not configured")
		return
	}
	hosts, err := s.HostRecords.ListHosts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "listing hosts")
		return
	}
	// ADR-0023: a signed-in person sees only the hosts their relation grants
	// view on. This is the aggregate route, so an unfiltered answer here would
	// hand over the full inventory of someone else's servers before any
	// per-host route was even called.
	hosts = s.visibleHosts(r, hosts)

	credentials, err := s.hostCredentials(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "listing host credentials")
		return
	}

	// The web UI consumes this endpoint as an array. A nil Go slice encodes as
	// JSON null, which is not an empty collection to JavaScript callers.
	entries := make([]HostListEntry, 0, len(hosts))
	for _, host := range hosts {
		entries = append(entries, hostListEntry(host, credentials[host.ID], time.Now().UTC()))
	}
	writeJSON(w, http.StatusOK, entries)
}

// hostCredentials indexes the newest credential of each host. A host whose
// token was rotated has more than one row, and the one that describes its
// current state is the last one issued.
func (s *Server) hostCredentials(ctx context.Context) (map[string]transport.TokenInfo, error) {
	if s.Tokens == nil {
		return nil, nil
	}
	tokens, err := s.Tokens.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	newest := make(map[string]transport.TokenInfo, len(tokens))
	for _, token := range tokens {
		if current, ok := newest[token.HostID]; ok && current.ID > token.ID {
			continue
		}
		newest[token.HostID] = token
	}
	return newest, nil
}

func hostListEntry(host schema.Host, credential transport.TokenInfo, now time.Time) HostListEntry {
	entry := HostListEntry{Host: host, Status: HostStatusNoCredential}
	if credential.ID == 0 {
		return entry
	}
	createdAt := credential.CreatedAt
	entry.CreatedAt = &createdAt
	if !credential.LastSeenAt.IsZero() {
		lastSeen := credential.LastSeenAt
		entry.TokenLastSeenAt = &lastSeen
	}
	switch {
	case credential.Revoked():
		entry.Status, entry.Revoked = HostStatusRevoked, true
	case credential.Expired(now):
		entry.Status = HostStatusExpired
	default:
		entry.Status = HostStatusActive
	}
	return entry
}

// RevokeHostResponse reports what a revocation actually did. Revoked is the
// number of credentials taken out of service, which is 0 for a host that was
// already revoked — a retried revocation is not an error.
type RevokeHostResponse struct {
	HostID  string `json:"host_id"`
	Revoked int    `json:"revoked"`
}

// RotateHostResponse carries the replacement credential. Token is readable
// only here, and GraceExpiresAt is when the credential it replaces stops
// working — the deadline for installing this one.
type RotateHostResponse struct {
	HostID         string     `json:"host_id"`
	Token          string     `json:"token"`
	CreatedAt      time.Time  `json:"created_at"`
	TokenPath      string     `json:"token_path"`
	GraceExpiresAt *time.Time `json:"grace_expires_at,omitempty"`
}

// handleRevokeHost implements POST /v1/hosts/{id}/revoke.
func (s *Server) handleRevokeHost(w http.ResponseWriter, r *http.Request) {
	hostID, ok := s.hostMutationTarget(w, r)
	if !ok {
		return
	}
	revoked, err := s.Tokens.RevokeHostTokens(r.Context(), hostID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "revoking host credentials")
		return
	}
	writeJSON(w, http.StatusOK, RevokeHostResponse{HostID: hostID, Revoked: revoked})
}

// handleRotateHost implements POST /v1/hosts/{id}/rotate. The old credential
// keeps working for DefaultRotationGrace unless the caller asks for an
// immediate cutover, which is the answer to a suspected compromise.
func (s *Server) handleRotateHost(w http.ResponseWriter, r *http.Request) {
	hostID, ok := s.hostMutationTarget(w, r)
	if !ok {
		return
	}

	var body struct {
		// GraceSeconds overrides the default window. A caller that sends 0
		// is asking for the old credential to die now.
		GraceSeconds *int `json:"grace_seconds"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	grace := DefaultRotationGrace
	if body.GraceSeconds != nil {
		if *body.GraceSeconds < 0 {
			writeJSONError(w, http.StatusBadRequest, "grace_seconds cannot be negative")
			return
		}
		grace = time.Duration(*body.GraceSeconds) * time.Second
	}

	token, err := newIngestToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating ingest token")
		return
	}
	rotated, err := s.Tokens.RotateHostTokens(r.Context(), hostID, "", token, grace)
	if errors.Is(err, transport.ErrNoCredential) {
		// ADR-0023 wants a host the caller cannot reach to look absent
		// rather than forbidden; a host that was never enrolled is absent
		// in the plainest sense.
		writeJSONError(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		// Any other failure is the store's, and reporting it as "not found"
		// would send an operator looking for a host that is right there.
		writeJSONError(w, http.StatusInternalServerError, "rotating host credentials")
		return
	}

	response := RotateHostResponse{
		HostID:    hostID,
		Token:     token,
		CreatedAt: rotated.CreatedAt,
		TokenPath: agentTokenPath,
	}
	if grace > 0 {
		expires := rotated.CreatedAt.Add(grace)
		response.GraceExpiresAt = &expires
	}
	writeJSON(w, http.StatusCreated, response)
}

// hostMutationTarget resolves and authorizes the host a credential mutation
// names. It answers the response itself on refusal, reporting ok=false.
func (s *Server) hostMutationTarget(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return "", false
	}
	if s.Tokens == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "host credential management is not configured")
		return "", false
	}
	hostID := r.PathValue("id")
	if hostID == "" {
		writeJSONError(w, http.StatusBadRequest, "host id is required")
		return "", false
	}
	if err := validateHostID(hostID); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	if !s.authorizeHostMutation(w, r, hostID) {
		return "", false
	}
	return hostID, true
}

// authorizeHostMutation guards every route that changes an ingest
// credential: enrolling a host, revoking one and rotating one.
//
// A signed-in person needs ADR-0023 `operate` on the host, exactly as a
// package action does — being allowed to look at a server is not being
// allowed to cut off its telemetry. Without a human boundary configured, the
// device token that POST /v1/hosts has always required is still the
// credential, so existing clients keep working.
//
// hostID is empty when the mutation does not name a host yet (creating a
// host, or an unpinned enrollment code). A human caller then needs operate
// on every host, because the host the grant would apply to does not exist.
func (s *Server) authorizeHostMutation(w http.ResponseWriter, r *http.Request, hostID string) bool {
	if s.Humans != nil {
		if _, ok := s.Humans.Identity(r); ok {
			access := s.hostAccessFor(r)
			if hostID == "" {
				if !access.scope.AllHosts.Operate {
					writeJSONError(w, http.StatusForbidden, "enrolling a host requires operate permission")
					return false
				}
				return true
			}
			// Answers 404, never 403: ADR-0023 treats a host the identity
			// cannot operate as one that does not exist.
			return !s.hostOutOfOperateScope(w, r, hostID)
		}
	}
	return s.hostMutationDeviceToken(w, r)
}

// hostMutationDeviceToken is the non-human path: the device token (ADR-0014)
// that POST /v1/hosts has always demanded.
//
// It is required even when no device has ever been paired. handleDevicePair
// lets the very first pairing through unauthenticated because there is no
// other way to bootstrap a reader; that exception must not extend here.
// Enrolling or rotating a host grants permanent *write* capability into the
// hub's storage, so an unauthenticated caller reaching these routes could
// inject arbitrary metrics, events and logs for a host of their choosing.
func (s *Server) hostMutationDeviceToken(w http.ResponseWriter, r *http.Request) bool {
	if s.Devices == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "this route requires device-token auth, which is not configured")
		return false
	}
	token, ok := bearerToken(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
		return false
	}
	valid, err := s.Devices.Lookup(r.Context(), token)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "checking device token")
		return false
	}
	if !valid {
		writeJSONError(w, http.StatusUnauthorized, "invalid device token")
		return false
	}
	return true
}

// newHostID mints the identity a brand-new host is enrolled with.
func newHostID() (string, error) {
	return ulid.Make().String(), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func newIngestToken() (string, error) {
	buf := make([]byte, ingestTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating ingest token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validateHostID accepts the id an already-running agent generated for
// itself, while refusing anything that isn't a plain identifier: host_id
// ends up in metric labels and storage keys, so shell metacharacters,
// path separators and whitespace have no business in it.
func validateHostID(id string) error {
	if len(id) > hostIDMaxLen {
		return fmt.Errorf("host_id is longer than %d characters", hostIDMaxLen)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("host_id may only contain letters, digits, '-', '_' and '.'")
		}
	}
	return nil
}
