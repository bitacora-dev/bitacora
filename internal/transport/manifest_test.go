package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recordingManifestStore struct {
	hostID, hostname, agentVersion string
	receivedAt                     time.Time
}

func (s *recordingManifestStore) RecordHostManifest(_ context.Context, hostID, hostname, agentVersion string, receivedAt time.Time) error {
	s.hostID, s.hostname, s.agentVersion, s.receivedAt = hostID, hostname, agentVersion, receivedAt
	return nil
}

func TestManifest_AuthenticatesTokenAndRecordsMetadata(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "ingest-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	store := &recordingManifestStore{}
	srv := &Server{Tokens: tokens, Manifests: store}
	req := httptest.NewRequest(http.MethodPost, "/v1/manifest", strings.NewReader(`{"host_id":"host-a","hostname":"web-01","agent_version":"1.2.3"}`))
	req.Header.Set("Authorization", "Bearer ingest-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if store.hostID != "host-a" || store.hostname != "web-01" || store.agentVersion != "1.2.3" || store.receivedAt.IsZero() {
		t.Fatalf("unexpected recorded manifest: %+v", store)
	}
}

func TestManifest_RejectsHostIDMismatchedWithToken(t *testing.T) {
	tokens := NewMemoryTokenStore()
	if err := tokens.AddToken("host-a", "ingest-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}
	srv := &Server{Tokens: tokens, Manifests: &recordingManifestStore{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/manifest", strings.NewReader(`{"host_id":"host-b","hostname":"web-01","agent_version":"1.2.3"}`))
	req.Header.Set("Authorization", "Bearer ingest-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
