package hubapi

import (
	"encoding/json"
	"net/http"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// humanLoginStatus is the reporting side of the human boundary: which login
// routes exist, and whether a configured local source is still waiting for its
// credential. It is a separate, optional interface so a bare Server{} and the
// narrow fakes that only need HasSession keep working.
type humanLoginStatus interface {
	LoginSources() (local, oidc bool)
	LocalPendingInitialization() bool
}

// authSessionResponse is the body of GET /v1/auth/session. It describes the
// hub's boundary and the caller's session — never the credential itself, so it
// stays safe to answer before anyone has signed in.
type authSessionResponse struct {
	// AuthEnabled distinguishes "no human boundary is configured" from
	// "configured, and you are not signed in".
	AuthEnabled                bool              `json:"auth_enabled"`
	Authenticated              bool              `json:"authenticated"`
	Identity                   *hubauth.Identity `json:"identity,omitempty"`
	LocalEnabled               bool              `json:"local_enabled"`
	OIDCEnabled                bool              `json:"oidc_enabled"`
	LocalPendingInitialization bool              `json:"local_pending_initialization"`
}

// handleAuthSession serves GET /v1/auth/session: the state of the hub's human
// authentication, from the point of view of this request.
//
// It answers 200 even without a session, on purpose. This is the route an
// operator reads to find out whether authentication is active at all, and a
// 401 there is indistinguishable from "the endpoint does not exist" for
// someone diagnosing a deployment — which is how local authentication stayed
// invisibly switched off. It reveals only which login routes the hub offers;
// /auth/me already reported the same two booleans to the login screen.
func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	response := authSessionResponse{AuthEnabled: s.Humans != nil}
	if s.Humans != nil {
		if identity, ok := s.Humans.Identity(r); ok {
			response.Authenticated = true
			response.Identity = &identity
		}
		if status, ok := s.Humans.(humanLoginStatus); ok {
			response.LocalEnabled, response.OIDCEnabled = status.LoginSources()
			response.LocalPendingInitialization = status.LocalPendingInitialization()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
