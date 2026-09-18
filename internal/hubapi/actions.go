package hubapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// actionRequest has intentionally no command, package, inventory, log, alert,
// tag, or free-text field. strictDecodeActionRequest rejects any attempted
// observed-data relay instead of ignoring it.
type actionRequest struct {
	HostID    string `json:"host_id"`
	Operation string `json:"operation"`
}

type actionConfirmationRequest struct {
	actionRequest
	RequestID   string `json:"request_id"`
	ActionToken string `json:"action_token"`
}

func (s *Server) handleActionToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity, ok := s.actionIdentity(w, r)
	if !ok {
		return
	}
	var body actionRequest
	if err := strictDecodeActionRequest(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	operation, ok := packageOperation(body.Operation)
	if !ok || body.HostID == "" {
		writeJSONError(w, http.StatusBadRequest, "host_id and a fixed package operation are required")
		return
	}
	issued, err := s.Actions.Issue(r.Context(), actionconfirm.IssueInput{
		Subject: identity.Subject, HostID: body.HostID, Operation: operation, NetworkOrigin: r.RemoteAddr,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "issuing action token")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"request_id":   issued.RequestID,
		"action_token": issued.Token,
		"expires_at":   issued.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (s *Server) handleActionConfirmation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity, ok := s.actionIdentity(w, r)
	if !ok {
		return
	}
	var body actionConfirmationRequest
	if err := strictDecodeActionRequest(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	operation, ok := packageOperation(body.Operation)
	if !ok || body.HostID == "" || body.RequestID == "" || body.ActionToken == "" {
		writeJSONError(w, http.StatusBadRequest, "host_id, operation, request_id and action_token are required")
		return
	}
	err := s.Actions.Confirm(r.Context(), actionconfirm.ConfirmInput{
		Subject: identity.Subject, HostID: body.HostID, Operation: operation, RequestID: body.RequestID,
		Token: body.ActionToken, NetworkOrigin: r.RemoteAddr,
	})
	if err != nil {
		status := http.StatusInternalServerError
		message := "confirming package operation"
		switch {
		case errors.Is(err, actionconfirm.ErrInvalidToken):
			status, message = http.StatusUnauthorized, "invalid action token"
		case errors.Is(err, actionconfirm.ErrTokenUsed):
			status, message = http.StatusConflict, "action token already used"
		case errors.Is(err, actionconfirm.ErrTokenExpired):
			status, message = http.StatusGone, "action token expired"
		}
		writeJSONError(w, status, message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}

func (s *Server) actionIdentity(w http.ResponseWriter, r *http.Request) (identity struct{ Subject string }, ok bool) {
	if s.Actions == nil || s.Humans == nil {
		writeJSONError(w, http.StatusNotFound, "package actions are disabled")
		return identity, false
	}
	human, ok := s.Humans.Identity(r)
	if !ok || human.Subject == "" {
		writeJSONError(w, http.StatusUnauthorized, "human authentication required")
		return identity, false
	}
	return struct{ Subject string }{Subject: human.Subject}, true
}

func strictDecodeActionRequest(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func packageOperation(value string) (bitacorapb.PackageOperation, bool) {
	switch value {
	case "REFRESH_PACKAGE_CACHE":
		return bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, true
	case "APPLY_PENDING_PACKAGE_UPDATES":
		return bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES, true
	default:
		return bitacorapb.PackageOperation_PACKAGE_OPERATION_UNSPECIFIED, false
	}
}
