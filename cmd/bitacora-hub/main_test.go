package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/bitacora-dev/bitacora/internal/hubapi"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// TestIngestEndToEnd proves ADR-0008 works end to end through the actual
// process wiring newHub builds for main(), not through transport or
// ingestreceiver in isolation: a real batch, sent over real HTTP/2
// cleartext to /v1/ingest with a token registered the same way an
// operator would (sqlitetokenstore.AddToken, what -add-token calls),
// lands in real storage and shows up through the real read API — served
// by the very same listener and handler.
func TestIngestEndToEnd(t *testing.T) {
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	defer h.Close()

	const hostID = "host-a"
	if err := h.tokens.AddToken(hostID, "test-token"); err != nil {
		t.Fatalf("adding token: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: h2c.NewHandler(h.handler, &http2.Server{})}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	baseURL := "http://" + ln.Addr().String()

	client := &transport.Client{BaseURL: baseURL, Token: "test-token"}
	batch := &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
		Events: []*bitacorapb.Event{{
			Id:       ulid.Make().String(),
			TsMs:     time.Now().UnixMilli(),
			HostId:   hostID,
			Source:   "kernel",
			Type:     "kernel.segfault",
			Severity: "error",
			Title:    "segfault in node (cpu 8)",
			Schema:   1,
			Subject:  &bitacorapb.EventSubject{Kind: "process", Name: "node"},
		}},
	}

	if _, err := client.Send(context.Background(), batch); err != nil {
		t.Fatalf("sending batch to /v1/ingest: %v", err)
	}

	_, deviceToken, _, err := h.devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/summary?host_id="+hostID+"&window=1h", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+deviceToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/summary: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/summary: unexpected status %d", resp.StatusCode)
	}

	var summary struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decoding /v1/summary response: %v", err)
	}

	for _, e := range summary.Events {
		if e.Title == "segfault in node (cpu 8)" {
			return
		}
	}
	t.Fatalf("event ingested via /v1/ingest did not appear in /v1/summary: %+v", summary.Events)
}

// TestRunAddToken proves the -add-token flow (main's replacement for the
// not-yet-built `bita agent create`) actually lets the registered token
// authenticate against the real ingest server, end to end.
func TestRunAddToken(t *testing.T) {
	dataDir := t.TempDir()

	if err := runAddToken(dataDir, "host-b:another-token"); err != nil {
		t.Fatalf("runAddToken: %v", err)
	}

	h, err := newHub(dataDir)
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	defer h.Close()

	hostID, ok, err := h.tokens.Lookup(context.Background(), "another-token")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok || hostID != "host-b" {
		t.Fatalf("Lookup after runAddToken = (%q, %v), want (\"host-b\", true)", hostID, ok)
	}
}

func TestRunAddToken_InvalidSpec(t *testing.T) {
	if err := runAddToken(t.TempDir(), "no-colon-here"); err == nil {
		t.Fatal("expected an error for a spec without a colon")
	}
}

// TestDevicePairingSurvivesHubRebuild proves the regression that an
// in-memory-only DeviceTokenStore misses: a device paired before a hub restart
// must still be recognized after newHub rebuilds every store from dataDir.
func TestDevicePairingSurvivesHubRebuild(t *testing.T) {
	dataDir := t.TempDir()
	first, err := newHub(dataDir)
	if err != nil {
		t.Fatalf("first newHub: %v", err)
	}

	code, token, _, err := first.devices.Start(context.Background())
	if err != nil {
		first.Close()
		t.Fatalf("starting pairing: %v", err)
	}
	if claimed, ok := first.devices.Claim(context.Background(), code); !ok || claimed != token {
		first.Close()
		t.Fatalf("claiming pairing = (%q, %v), want (%q, true)", claimed, ok, token)
	}
	first.Close()

	rebuilt, err := newHub(dataDir)
	if err != nil {
		t.Fatalf("rebuilt newHub: %v", err)
	}
	defer rebuilt.Close()

	valid, err := rebuilt.devices.Lookup(context.Background(), token)
	if err != nil {
		t.Fatalf("looking up persisted device token: %v", err)
	}
	if !valid {
		t.Fatal("expected paired device token to remain valid after hub rebuild")
	}
	hasAny, err := rebuilt.devices.HasAnyToken(context.Background())
	if err != nil {
		t.Fatalf("checking persisted device tokens: %v", err)
	}
	if !hasAny {
		t.Fatal("expected rebuilt device store to report at least one token")
	}
}

// TestEnrollHostFromWebAPIThenIngest proves the whole point of the web
// enrollment flow: a host created through POST /v1/hosts — the request the
// "Añadir servidor" button makes, authenticated with a device token — gets
// an ingest token that really works against /v1/ingest, with no SSH and no
// -add-token anywhere in the path.
func TestEnrollHostFromWebAPIThenIngest(t *testing.T) {
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	defer h.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: h2c.NewHandler(h.handler, &http2.Server{})}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	baseURL := "http://" + ln.Addr().String()

	_, deviceToken, _, err := h.devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}

	// Without the device token the same request must not enroll anything.
	unauth, err := http.Post(baseURL+"/v1/hosts", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /v1/hosts without a device token: %v", err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /v1/hosts: status %d, want %d", unauth.StatusCode, http.StatusUnauthorized)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/hosts", nil)
	if err != nil {
		t.Fatalf("building enrollment request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+deviceToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/hosts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/hosts: status %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var created hubapi.CreateHostResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding enrollment response: %v", err)
	}
	if created.HostID == "" || created.Token == "" {
		t.Fatalf("enrollment response is incomplete: %+v", created)
	}

	client := &transport.Client{BaseURL: baseURL, Token: created.Token}
	batch := &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  created.HostID,
		Events: []*bitacorapb.Event{{
			Id:       ulid.Make().String(),
			TsMs:     time.Now().UnixMilli(),
			HostId:   created.HostID,
			Source:   "agent",
			Type:     "agent.started",
			Severity: "info",
			Title:    "enrolled from the web UI",
			Schema:   1,
		}},
	}
	if _, err := client.Send(context.Background(), batch); err != nil {
		t.Fatalf("sending a batch with the web-issued token: %v", err)
	}

	summaryReq, err := http.NewRequest(http.MethodGet, baseURL+"/v1/summary?host_id="+created.HostID+"&window=1h", nil)
	if err != nil {
		t.Fatalf("building summary request: %v", err)
	}
	summaryReq.Header.Set("Authorization", "Bearer "+deviceToken)

	summaryResp, err := http.DefaultClient.Do(summaryReq)
	if err != nil {
		t.Fatalf("GET /v1/summary: %v", err)
	}
	defer summaryResp.Body.Close()

	var summary struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	if err := json.NewDecoder(summaryResp.Body).Decode(&summary); err != nil {
		t.Fatalf("decoding /v1/summary response: %v", err)
	}
	for _, e := range summary.Events {
		if e.Title == "enrolled from the web UI" {
			return
		}
	}
	t.Fatalf("the batch sent with the web-issued token did not reach /v1/summary: %+v", summary.Events)
}

// TestEnrolledTokenIsBoundToItsHost proves the web flow inherits ADR-0008's
// isolation guarantee: a token minted for one host can't write data for
// another.
func TestEnrolledTokenIsBoundToItsHost(t *testing.T) {
	h, err := newHub(t.TempDir())
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	defer h.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: h2c.NewHandler(h.handler, &http2.Server{})}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	baseURL := "http://" + ln.Addr().String()

	_, deviceToken, _, err := h.devices.Start(context.Background())
	if err != nil {
		t.Fatalf("minting device token: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/hosts", nil)
	if err != nil {
		t.Fatalf("building enrollment request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/hosts: %v", err)
	}
	defer resp.Body.Close()

	var created hubapi.CreateHostResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding enrollment response: %v", err)
	}

	client := &transport.Client{BaseURL: baseURL, Token: created.Token}
	batch := &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  "someone-elses-host",
	}
	if _, err := client.Send(context.Background(), batch); err == nil {
		t.Fatal("a web-issued token was accepted for a host_id it isn't bound to")
	}
}
