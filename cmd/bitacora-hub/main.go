// bitacora-hub ingests, stores, evaluates alerts and serves the read API,
// the web UI (ADR-0002) and the agent-facing ingest endpoint (ADR-0008) —
// all in the same process, over the same listener.
//
// Registering an ingest token for a host is normally done from the web UI
// ("Añadir servidor", POST /v1/hosts). This flag is the offline equivalent
// for when the UI isn't reachable — for instance before any device has
// been paired:
//
//	bitacora-hub -data-dir=/var/lib/bitacora -add-token=<host_id>:<token-en-texto-plano>
//
// Both paths write the token's Argon2id hash to the same persistent
// SQLite token store; -add-token exits without starting the server.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/bitacora-dev/bitacora/internal/hubapi"
	"github.com/bitacora-dev/bitacora/internal/ingestreceiver"
	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/storage"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/internal/transport/sqlitetokenstore"
	"github.com/bitacora-dev/bitacora/internal/webui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address (ADR-0002: agent talks to hub here by default)")
	dataDir := flag.String("data-dir", "/var/lib/bitacora", "base directory for hub data")
	addToken := flag.String("add-token", "", "register an ingest token and exit, without starting the server: <host_id>:<token-en-texto-plano>")
	flag.Parse()

	if *addToken != "" {
		if err := runAddToken(*dataDir, *addToken); err != nil {
			log.Fatal(err)
		}
		return
	}

	h, err := newHub(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()

	log.Printf("bitacora-hub listening on %s (data: %s)", *addr, *dataDir)
	httpSrv := &http.Server{
		Addr:    *addr,
		Handler: h2c.NewHandler(h.handler, &http2.Server{}),
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// runAddToken registers an ingest token in the persistent SQLite store
// without starting the server.
func runAddToken(dataDir, spec string) error {
	hostID, token, ok := strings.Cut(spec, ":")
	if !ok || hostID == "" || token == "" {
		return fmt.Errorf("invalid -add-token value %q: want <host_id>:<token>", spec)
	}

	store, err := sqlitetokenstore.New(tokenStorePath(dataDir))
	if err != nil {
		return fmt.Errorf("opening token store: %w", err)
	}
	defer store.Close()

	if err := store.AddToken(hostID, token); err != nil {
		return fmt.Errorf("adding token: %w", err)
	}

	fmt.Printf("token added for host %q\n", hostID)
	return nil
}

func tokenStorePath(dataDir string) string {
	return filepath.Join(dataDir, "tokens.db")
}

// hub bundles the merged HTTP handler (read API + web UI + /v1/ingest)
// with the stores newHub opened for it, so main and tests can build the
// full wiring once and close it the same way.
type hub struct {
	handler http.Handler
	tokens  *sqlitetokenstore.Store
	devices *hubapi.DeviceTokenStore
	closers []io.Closer
}

func (h *hub) Close() {
	for _, c := range h.closers {
		if err := c.Close(); err != nil {
			log.Printf("closing store: %v", err)
		}
	}
}

// newHub wires the read API and web UI (hubapi.Server) and the real
// /v1/ingest endpoint (transport.Server, per ADR-0008) against storage
// rooted at dataDir, merged into a single handler served by one listener.
func newHub(dataDir string) (*hub, error) {
	relStore, err := storage.NewSQLiteStore(filepath.Join(dataDir, "db"))
	if err != nil {
		return nil, fmt.Errorf("opening relational store: %w", err)
	}

	metricsStore, err := metricstore.Open(
		filepath.Join(dataDir, "metrics", string(metricstore.ResolutionRaw)),
		metricstore.DefaultRetention[metricstore.ResolutionRaw],
	)
	if err != nil {
		relStore.Close()
		return nil, fmt.Errorf("opening metric store: %w", err)
	}

	logStore := logstore.NewStore(filepath.Join(dataDir, "logs"))

	tokenStore, err := sqlitetokenstore.New(tokenStorePath(dataDir))
	if err != nil {
		relStore.Close()
		metricsStore.Close()
		return nil, fmt.Errorf("opening token store: %w", err)
	}

	devices := hubapi.NewDeviceTokenStore()
	readSrv := &hubapi.Server{
		Metrics:     metricsStore,
		Events:      relStore,
		Inventories: relStore,
		WebUI:       webui.FS(),
		Devices:     devices,
		// Same store -add-token writes to: enrolling a host from the web
		// UI (POST /v1/hosts) and from the CLI must produce exactly the
		// same persisted Argon2id hash, not two parallel registries.
		Hosts: tokenStore,
	}

	ingestSrv := &transport.Server{
		Tokens:      tokenStore,
		Idempotency: transport.NewMemoryIdempotencyStore(),
		Receiver:    ingestreceiver.New(metricsStore, relStore, logStore),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", ingestSrv.Handler())
	mux.Handle("/", readSrv.Handler())

	return &hub{
		handler: mux,
		tokens:  tokenStore,
		devices: devices,
		closers: []io.Closer{relStore, metricsStore, tokenStore},
	}, nil
}
