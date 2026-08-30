// bitacora-hub ingests, stores, evaluates alerts and serves the read API
// and web UI (ADR-0002). This is the minimal read-path wiring — GET
// /v1/summary and the embedded dashboard (ADR-0014) — over real SQLite and
// tsdb storage. POST /v1/ingest (ADR-0008) is not wired in here yet; see
// internal/transport for the standalone endpoint implementation.
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"

	"github.com/bitacora-dev/bitacora/internal/hubapi"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/storage"
	"github.com/bitacora-dev/bitacora/internal/webui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address (ADR-0002: agent talks to hub here by default)")
	dataDir := flag.String("data-dir", "/var/lib/bitacora", "base directory for hub data")
	flag.Parse()

	relStore, err := storage.NewSQLiteStore(filepath.Join(*dataDir, "db"))
	if err != nil {
		log.Fatalf("opening relational store: %v", err)
	}
	defer relStore.Close()

	metricsStore, err := metricstore.Open(
		filepath.Join(*dataDir, "metrics", string(metricstore.ResolutionRaw)),
		metricstore.DefaultRetention[metricstore.ResolutionRaw],
	)
	if err != nil {
		log.Fatalf("opening metric store: %v", err)
	}
	defer metricsStore.Close()

	srv := &hubapi.Server{
		Metrics:     metricsStore,
		Events:      relStore,
		Inventories: relStore,
		WebUI:       webui.FS(),
		Devices:     hubapi.NewDeviceTokenStore(),
	}

	log.Printf("bitacora-hub listening on %s (data: %s)", *addr, *dataDir)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
