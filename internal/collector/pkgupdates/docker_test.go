package pkgupdates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type imageListEntry struct {
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
}

func TestDockerItems_DetectsNewerRegistryDigest(t *testing.T) {
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/nginx/manifests/1.25" {
			w.Header().Set("Docker-Content-Digest", "sha256:newdigest")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer registryServer.Close()

	// Using the registry test server's own address as the image's
	// registry host (e.g. "127.0.0.1:54321/nginx:1.25") means
	// parseImageReference resolves it exactly like a real self-hosted
	// registry, with no need to fake out Docker Hub's real hostname.
	repoTag := registryServer.Listener.Addr().String() + "/nginx:1.25"

	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]imageListEntry{
			{RepoTags: []string{repoTag}, RepoDigests: []string{"nginx@sha256:olddigest"}},
		})
	}))
	defer metadataServer.Close()

	reg := &registryClient{HTTPClient: registryServer.Client(), Scheme: "http"}
	items := dockerItems(context.Background(), metadataServer.URL, reg)
	if len(items) != 1 {
		t.Fatalf("expected 1 outdated image, got %d: %+v", len(items), items)
	}
	if items[0].Attrs["current_digest"] != "sha256:olddigest" {
		t.Fatalf("unexpected current_digest: %+v", items[0].Attrs)
	}
	if items[0].Attrs["registry_digest"] != "sha256:newdigest" {
		t.Fatalf("unexpected registry_digest: %+v", items[0].Attrs)
	}
}

func TestDockerItems_SameDigestIsNotOutdated(t *testing.T) {
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:samedigest")
		w.WriteHeader(http.StatusOK)
	}))
	defer registryServer.Close()

	repoTag := registryServer.Listener.Addr().String() + "/nginx:1.25"
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]imageListEntry{
			{RepoTags: []string{repoTag}, RepoDigests: []string{"nginx@sha256:samedigest"}},
		})
	}))
	defer metadataServer.Close()

	reg := &registryClient{HTTPClient: registryServer.Client(), Scheme: "http"}
	items := dockerItems(context.Background(), metadataServer.URL, reg)
	if len(items) != 0 {
		t.Fatalf("expected no outdated images when digests match, got %+v", items)
	}
}

func TestDockerItems_RegistryUnreachableDegradesGracefully(t *testing.T) {
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]imageListEntry{
			{RepoTags: []string{"127.0.0.1:1/nginx:1.25"}, RepoDigests: []string{"nginx@sha256:olddigest"}},
		})
	}))
	defer metadataServer.Close()

	reg := &registryClient{HTTPClient: http.DefaultClient, Scheme: "http"}
	items := dockerItems(context.Background(), metadataServer.URL, reg)
	if items != nil {
		t.Fatalf("expected an unreachable registry to degrade to no item, not an error, got %+v", items)
	}
}

func TestDockerItems_NoMetadataURLYieldsNoItemsNotError(t *testing.T) {
	items := dockerItems(context.Background(), "", &registryClient{HTTPClient: http.DefaultClient})
	if items != nil {
		t.Fatalf("expected nil items without docker-socket-proxy configured, got %+v", items)
	}
}

func TestDockerItems_MetadataProxyDownDegradesGracefully(t *testing.T) {
	items := dockerItems(context.Background(), "http://127.0.0.1:1", &registryClient{HTTPClient: http.DefaultClient})
	if items != nil {
		t.Fatalf("expected nil items when the metadata proxy is unreachable, got %+v", items)
	}
}
