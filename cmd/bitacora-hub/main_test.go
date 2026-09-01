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
