package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// HostManifestRecorder persists the identity fields that the hub needs to
// list agents. The capability payload remains agent-owned; this endpoint only
// records the host metadata used by the host selector.
type HostManifestRecorder interface {
	RecordHostManifest(ctx context.Context, hostID, hostname, agentVersion string, receivedAt time.Time) error
}

type manifestRequest struct {
	HostID       string `json:"host_id"`
	Hostname     string `json:"hostname"`
	AgentVersion string `json:"agent_version"`
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Manifests == nil {
		http.Error(w, "manifest storage is not configured", http.StatusServiceUnavailable)
		return
	}

	token, ok := bearerToken(r)
	if !ok {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	hostID, ok, err := s.Tokens.Lookup(r.Context(), token)
	if err != nil {
		http.Error(w, "auth error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	var manifest manifestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, DefaultMaxBodyBytes)).Decode(&manifest); err != nil {
		http.Error(w, "invalid manifest", http.StatusBadRequest)
		return
	}
	if manifest.HostID == "" || manifest.Hostname == "" || manifest.AgentVersion == "" {
		http.Error(w, "host_id, hostname and agent_version are required", http.StatusBadRequest)
		return
	}
	if manifest.HostID != hostID {
		http.Error(w, "manifest host_id does not match token", http.StatusForbidden)
		return
	}
	if err := s.Manifests.RecordHostManifest(r.Context(), hostID, manifest.Hostname, manifest.AgentVersion, time.Now().UTC()); err != nil {
		http.Error(w, "storing manifest", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
