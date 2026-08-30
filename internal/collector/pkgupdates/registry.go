package pkgupdates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// registryRef is a parsed Docker image reference: which registry host to
// talk to, the repository path within it, and the tag to check.
type registryRef struct {
	Host       string
	Repository string
	Reference  string
}

// parseImageReference parses a "repo:tag" image reference the way the
// Docker CLI does: the tag separator is the LAST ":" that appears AFTER
// the last "/" — a "host:port/repo" prefix's ":" must never be mistaken
// for the tag separator. The registry host is then whatever precedes the
// first "/", if that segment looks like a host (contains "." or ":", or
// is "localhost"); otherwise the image is implicitly on Docker Hub, with
// "library/" prefixed for an unqualified single-segment name (e.g.
// "nginx" -> "library/nginx", Docker Hub's own namespace for official
// images).
func parseImageReference(ref string) (registryRef, bool) {
	if ref == "" {
		return registryRef{}, false
	}

	name, tag := ref, "latest"
	tail := ref
	if lastSlash := strings.LastIndexByte(ref, '/'); lastSlash >= 0 {
		tail = ref[lastSlash+1:]
	}
	if idx := strings.IndexByte(tail, ':'); idx >= 0 {
		tag = tail[idx+1:]
		name = ref[:len(ref)-len(tail)+idx]
	}

	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 && looksLikeHost(parts[0]) {
		return registryRef{Host: parts[0], Repository: parts[1], Reference: tag}, true
	}

	repo := name
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return registryRef{Host: "registry-1.docker.io", Repository: repo, Reference: tag}, true
}

func looksLikeHost(s string) bool {
	return s == "localhost" || strings.Contains(s, ".") || strings.Contains(s, ":")
}

// registryClient checks a repository's current manifest digest against a
// registry implementing the standard OCI/Docker Registry v2 distribution
// API — the same auth-challenge flow (HEAD manifest -> 401 with
// WWW-Authenticate -> fetch a bearer token -> retry) that Docker Hub,
// GHCR and most other registries all implement, so this isn't
// Docker-Hub-specific.
type registryClient struct {
	HTTPClient *http.Client
	// Scheme is "https" in production; tests override it to "http" to
	// talk to an httptest.Server without a TLS handshake.
	Scheme string
}

const acceptManifests = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.oci.image.index.v1+json"

// Digest returns the registry's current Docker-Content-Digest for ref. A
// registry that's unreachable, rate limiting, or requires credentials
// this project doesn't have degrades to ok=false — ADR-0017 explicitly
// accepts some images ending up "no se pudo comprobar" rather than
// forcing authentication.
func (c *registryClient) Digest(ctx context.Context, ref registryRef) (string, bool) {
	scheme := c.Scheme
	if scheme == "" {
		scheme = "https"
	}
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, ref.Host, ref.Repository, ref.Reference)

	digest, status, wwwAuth, ok := c.headManifest(ctx, manifestURL, "")
	if ok {
		return digest, true
	}
	if status != http.StatusUnauthorized || wwwAuth == "" {
		return "", false
	}

	token, ok := c.fetchToken(ctx, wwwAuth)
	if !ok {
		return "", false
	}

	digest, _, _, ok = c.headManifest(ctx, manifestURL, token)
	return digest, ok
}

func (c *registryClient) headManifest(ctx context.Context, manifestURL, token string) (digest string, status int, wwwAuth string, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", 0, "", false
	}
	req.Header.Set("Accept", acceptManifests)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", 0, "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		d := resp.Header.Get("Docker-Content-Digest")
		return d, resp.StatusCode, "", d != ""
	}
	return "", resp.StatusCode, resp.Header.Get("Www-Authenticate"), false
}

var bearerParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// fetchToken implements the Bearer token exchange the WWW-Authenticate
// challenge describes (the distribution spec's auth flow): realm,
// service and scope all come from the challenge itself, never assumed.
func (c *registryClient) fetchToken(ctx context.Context, wwwAuthenticate string) (string, bool) {
	if !strings.HasPrefix(wwwAuthenticate, "Bearer ") {
		return "", false
	}
	params := map[string]string{}
	for _, m := range bearerParamRe.FindAllStringSubmatch(wwwAuthenticate, -1) {
		params[m[1]] = m[2]
	}
	realm, ok := params["realm"]
	if !ok {
		return "", false
	}

	u, err := url.Parse(realm)
	if err != nil {
		return "", false
	}
	q := u.Query()
	if service, ok := params["service"]; ok {
		q.Set("service", service)
	}
	if scope, ok := params["scope"]; ok {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	if body.Token != "" {
		return body.Token, true
	}
	if body.AccessToken != "" {
		return body.AccessToken, true
	}
	return "", false
}
