package capabilities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_SendPostsJSONToManifestEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody Manifest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, Token: "secret-token"}
	m := Manifest{HostID: "01HOST", Hostname: "myhost", ReportedAt: time.Now(), AgentVersion: "0.1.0"}

	if err := client.Send(context.Background(), m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != ManifestPath {
		t.Errorf("expected path %q, got %q", ManifestPath, gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("expected bearer token header, got %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected JSON content type, got %q", gotContentType)
	}
	if gotBody.HostID != m.HostID {
		t.Errorf("expected host_id %q to round-trip, got %q", m.HostID, gotBody.HostID)
	}
}

func TestClient_SendReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL}
	err := client.Send(context.Background(), Manifest{})
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}
