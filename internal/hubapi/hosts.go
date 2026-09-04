package hubapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/oklog/ulid/v2"
)

// hostIDMaxLen bounds an operator-supplied host_id. A generated one is a
// 26-character ULID (schema.LoadOrCreateHostID); the extra room is for
// hosts enrolled with an id that came from somewhere else.
const hostIDMaxLen = 64

// ingestTokenBytes is the entropy behind a generated ingest token, same
// size as the device tokens minted in devicetoken.go.
const ingestTokenBytes = 32

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
// Auth: a valid device token (ADR-0014) is always required — including
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
	if s.Devices == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "host enrollment requires device-token auth, which is not configured")
		return
	}
	if s.Hosts == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "host enrollment is not configured")
		return
	}

	token, ok := bearerToken(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	valid, err := s.Devices.Lookup(r.Context(), token)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "checking device token")
		return
	}
	if !valid {
		writeJSONError(w, http.StatusUnauthorized, "invalid device token")
		return
	}

	var body struct {
		HostID string `json:"host_id"`
		Name   string `json:"name"`
	}
	// An empty body is the common case ("give me a brand new host"), and
	// http.Request.Body yields io.EOF for it, so that isn't an error.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	hostID := body.HostID
	if hostID == "" {
		hostID = ulid.Make().String()
	} else if err := validateHostID(hostID); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	ingestToken, err := newIngestToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating ingest token")
		return
	}

	if err := s.Hosts.AddToken(hostID, ingestToken); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "registering ingest token")
		return
	}
	if s.HostRecords != nil {
		if err := s.HostRecords.CreateHost(r.Context(), hostID, body.Name); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "storing host")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(CreateHostResponse{
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hosts)
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
