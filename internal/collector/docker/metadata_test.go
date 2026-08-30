package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetadataClient_Images_FiltersDanglingAndUnpulledImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]imageListEntry{
			{RepoTags: []string{"nginx:1.25"}, RepoDigests: []string{"nginx@sha256:aaaa"}},
			{RepoTags: []string{"<none>:<none>"}, RepoDigests: nil},   // dangling
			{RepoTags: []string{"local/built:dev"}, RepoDigests: nil}, // built locally, never pulled
			{RepoTags: []string{"grafana/grafana:10.0"}, RepoDigests: []string{"grafana/grafana@sha256:bbbb"}},
		})
	}))
	defer server.Close()

	images, err := NewMetadataClient(server.URL).Images(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 comparable images, got %d: %+v", len(images), images)
	}

	byTag := map[string]string{}
	for _, img := range images {
		byTag[img.RepoTag] = img.RepoDigest
	}
	if byTag["nginx:1.25"] != "sha256:aaaa" {
		t.Fatalf("unexpected nginx digest: %+v", byTag)
	}
	if byTag["grafana/grafana:10.0"] != "sha256:bbbb" {
		t.Fatalf("unexpected grafana digest: %+v", byTag)
	}
}

func TestMetadataClient_Images_ProxyDownReturnsError(t *testing.T) {
	_, err := NewMetadataClient("http://127.0.0.1:1").Images(context.Background())
	if err == nil {
		t.Fatal("expected an error when the metadata proxy is unreachable")
	}
}
