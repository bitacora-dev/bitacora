package pkgupdates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseImageReference(t *testing.T) {
	cases := map[string]registryRef{
		"nginx":                        {Host: "registry-1.docker.io", Repository: "library/nginx", Reference: "latest"},
		"nginx:1.25":                   {Host: "registry-1.docker.io", Repository: "library/nginx", Reference: "1.25"},
		"grafana/grafana:10.0":         {Host: "registry-1.docker.io", Repository: "grafana/grafana", Reference: "10.0"},
		"ghcr.io/owner/image:tag":      {Host: "ghcr.io", Repository: "owner/image", Reference: "tag"},
		"localhost:5000/foo:latest":    {Host: "localhost:5000", Repository: "foo", Reference: "latest"},
		"registry.example.com/ns/repo": {Host: "registry.example.com", Repository: "ns/repo", Reference: "latest"},
	}
	for input, want := range cases {
		got, ok := parseImageReference(input)
		if !ok {
			t.Errorf("parseImageReference(%q): expected ok=true", input)
			continue
		}
		if got != want {
			t.Errorf("parseImageReference(%q) = %+v, want %+v", input, got, want)
		}
	}
}

func TestRegistryClient_Digest_AnonymousAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/v2/library/nginx/manifests/1.25" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:aaaa")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := &registryClient{HTTPClient: server.Client(), Scheme: "http"}
	ref := registryRef{Host: serverHost(server), Repository: "library/nginx", Reference: "1.25"}
	digest, ok := c.Digest(context.Background(), ref)
	if !ok {
		t.Fatal("expected a successful anonymous digest check")
	}
	if digest != "sha256:aaaa" {
		t.Fatalf("unexpected digest: %q", digest)
	}
}

func TestRegistryClient_Digest_BearerTokenChallenge(t *testing.T) {
	var authServer *httptest.Server
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer real-token" {
			w.Header().Set("Docker-Content-Digest", "sha256:bbbb")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Www-Authenticate", `Bearer realm="`+authServer.URL+`/token",service="registry.example.com",scope="repository:owner/image:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer registryServer.Close()

	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("service") != "registry.example.com" {
			http.Error(w, "missing service param", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"token":"real-token"}`))
	}))
	defer authServer.Close()

	c := &registryClient{HTTPClient: registryServer.Client(), Scheme: "http"}
	ref := registryRef{Host: serverHost(registryServer), Repository: "owner/image", Reference: "latest"}
	digest, ok := c.Digest(context.Background(), ref)
	if !ok {
		t.Fatal("expected the bearer-token challenge flow to succeed")
	}
	if digest != "sha256:bbbb" {
		t.Fatalf("unexpected digest: %q", digest)
	}
}

func TestRegistryClient_Digest_UnreachableDegradesGracefully(t *testing.T) {
	c := &registryClient{HTTPClient: http.DefaultClient}
	ref := registryRef{Host: "127.0.0.1:1", Repository: "foo/bar", Reference: "latest"}
	if _, ok := c.Digest(context.Background(), ref); ok {
		t.Fatal("expected an unreachable registry to degrade to ok=false, not succeed")
	}
}

// serverHost returns the bare host[:port] an httptest.Server listens on —
// registryRef.Host never carries a scheme; Digest prepends one itself
// (registryClient.Scheme, overridden to "http" in these tests so no TLS
// handshake against the plain-HTTP test server is needed).
func serverHost(s *httptest.Server) string {
	return s.Listener.Addr().String()
}
