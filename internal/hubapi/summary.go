// Package hubapi implements the hub's read-facing API: JSON over HTTP,
// unlike the agent-facing ingest endpoint (ADR-0008 keeps Protobuf there
// and JSON here, "donde el volumen es pequeño y la depuración importa
// más").
package hubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DefaultWindow is how far back GET /v1/summary looks when the caller
// doesn't specify ?window=.
const DefaultWindow = 15 * time.Minute

// MetricQuerier is the read side of a metricstore.Store — narrowed to
// what Summary needs, so hubapi doesn't require a full metricstore.Store
// (a fake is enough in tests).
type MetricQuerier interface {
	Query(ctx context.Context, name string, from, to time.Time, extra ...*labels.Matcher) ([]metricstore.Sample, error)
}

// EventLister is the read side of storage.Relational that Summary needs.
type EventLister interface {
	ListEvents(ctx context.Context, from, to time.Time, hostID string) ([]schema.Event, error)
}

// InventoryGetter is the read side of storage.Relational that
// GET /v1/inventory needs (ADR-0015).
type InventoryGetter interface {
	GetInventory(ctx context.Context, hostID string, kind schema.InventoryKind) (schema.Inventory, bool, error)
}

// Server serves the hub's read API and the embedded web UI.
type Server struct {
	Metrics MetricQuerier
	Events  EventLister
	// Inventories serves GET /v1/inventory (ADR-0015). Nil means that
	// route always answers 404 — same "not wired everywhere yet" state
	// as Metrics/Events had before real storage existed.
	Inventories InventoryGetter
	// WebUI is the built frontend (ADR-0001: React+Vite+Tailwind+uPlot,
	// embedded via go:embed). Nil disables serving it — useful for
	// testing the API in isolation.
	WebUI fs.FS
	// Devices holds device tokens and pairing state (ADR-0014). Nil
	// disables device-token auth entirely — handleSummary is served
	// unauthenticated, which keeps existing callers that build a bare
	// Server{} working, and the pairing endpoints answer 503.
	Devices *DeviceTokenStore
	// Hosts registers ingest tokens for POST /v1/hosts (ADR-0008), so a
	// new machine can be enrolled from the web UI instead of over SSH
	// with `bitacora-hub -add-token`. Nil makes that route answer 503;
	// it never falls back to an unauthenticated or no-op path.
	Hosts HostRegistrar
}

// Handler returns the http.Handler serving /v1/summary (device-token
// authenticated when Devices is set), host enrollment (always device-token
// authenticated), the pairing bootstrap endpoints, and, if WebUI is set,
// the single-page UI at "/".
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/summary", s.requireDeviceToken(s.handleSummary))
	mux.HandleFunc("/v1/inventory", s.requireDeviceToken(s.handleInventory))
	mux.HandleFunc("/v1/hosts", s.handleCreateHost)
	mux.HandleFunc("/v1/devices/pair", s.handleDevicePair)
	mux.HandleFunc("/v1/devices/claim", s.handleDeviceClaim)
	if s.WebUI != nil {
		mux.Handle("/", http.FileServer(http.FS(s.WebUI)))
	}
	return mux
}

// requireDeviceToken guards a /v1/* data route with device-token auth.
// The pairing endpoints below deliberately don't go through this: they're
// the bootstrap path an unpaired device uses to get a token in the first
// place. handleDevicePair itself gates every pairing after the very first
// one — see its own comment for why: relying solely on network-level
// isolation (ADR-0014's original assumption) isn't safe once the hub is
// reachable from outside that network, which happens in practice.
func (s *Server) requireDeviceToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Devices == nil {
			next(w, r)
			return
		}

		token, ok := bearerToken(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		valid, err := s.Devices.Lookup(r.Context(), token)
		if err != nil || !valid {
			writeJSONError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleDevicePair implements POST /v1/devices/pair: mints a pairing code
// and device token for the QR flow (ADR-0014).
//
// Every pairing after the very first one requires the request to already
// present a valid device token. Without this, anyone who can reach the
// hub over the network — not just someone with an existing paired device
// — could call this endpoint directly (bypassing the QR/UI entirely) and
// mint themselves a working device token for free. The very first
// pairing is let through unauthenticated on purpose: with an empty
// store there's no existing device to present a token from, and the
// operator needs a way to pair their own first device right after
// deploying.
func (s *Server) handleDevicePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Devices == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "device pairing is not configured")
		return
	}

	if s.Devices.HasAnyToken() {
		token, ok := bearerToken(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		valid, err := s.Devices.Lookup(r.Context(), token)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "checking device token")
			return
		}
		if !valid {
			writeJSONError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
	}

	code, _, expiresAt, err := s.Devices.Start(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "starting pairing")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"code":       code,
		"expires_at": expiresAt.Format(time.RFC3339),
		"pair_path":  "/?pair=" + code,
	})
}

// handleDeviceClaim implements POST /v1/devices/claim: exchanges a
// pairing code, scanned from the QR, for the device token (ADR-0014).
func (s *Server) handleDeviceClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Devices == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "device pairing is not configured")
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	token, ok := s.Devices.Claim(r.Context(), body.Code)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "pairing code not found, expired, or already claimed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

// SeriesPoint is one timestamped value in a Summary series.
type SeriesPoint struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

// Summary is GET /v1/summary's response: everything the single-page
// timeline view needs to render, in one call (ADR-0014: "el endpoint
// GET /v1/summary?host_id=... debe devolver todo lo necesario para
// pintar la pantalla principal en una sola petición").
type Summary struct {
	HostID      string         `json:"host_id"`
	GeneratedAt time.Time      `json:"generated_at"`
	WindowSecs  float64        `json:"window_secs"`
	CPU         []SeriesPoint  `json:"cpu"`
	Memory      []SeriesPoint  `json:"memory"`
	Events      []schema.Event `json:"events"`
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hostID := r.URL.Query().Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id is required", http.StatusBadRequest)
		return
	}

	window := DefaultWindow
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid window: %v", err), http.StatusBadRequest)
			return
		}
		window = d
	}

	now := time.Now()
	from := now.Add(-window)
	hostMatcher := labels.MustNewMatcher(labels.MatchEqual, "host_id", hostID)

	cpu, err := s.Metrics.Query(r.Context(), "bitacora_cpu_usage_ratio", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying cpu metrics", http.StatusInternalServerError)
		return
	}
	mem, err := s.Metrics.Query(r.Context(), "bitacora_memory_used_ratio", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory metrics", http.StatusInternalServerError)
		return
	}
	events, err := s.Events.ListEvents(r.Context(), from, now, hostID)
	if err != nil {
		http.Error(w, "querying events", http.StatusInternalServerError)
		return
	}

	summary := Summary{
		HostID:      hostID,
		GeneratedAt: now,
		WindowSecs:  window.Seconds(),
		CPU:         toSeries(cpu),
		Memory:      toSeries(mem),
		Events:      events,
	}
	if summary.Events == nil {
		summary.Events = []schema.Event{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		// Headers are already sent at this point; nothing more to do but
		// log server-side in a real deployment. Nothing to log to yet.
		return
	}
}

// handleInventory implements GET /v1/inventory?host_id=...&kind=...
// (ADR-0015). A host/kind that's never been reported answers 404, not an
// empty Inventory — the caller needs to tell "nothing reported yet" apart
// from "reported, and it's an empty list" (a host with zero shares
// configured is a valid, meaningful snapshot).
func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Inventories == nil {
		http.Error(w, "inventory not available", http.StatusNotFound)
		return
	}

	hostID := r.URL.Query().Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id is required", http.StatusBadRequest)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		http.Error(w, "kind is required", http.StatusBadRequest)
		return
	}

	inv, ok, err := s.Inventories.GetInventory(r.Context(), hostID, schema.InventoryKind(kind))
	if err != nil {
		http.Error(w, "querying inventory", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no inventory reported for this host/kind", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(inv)
}

func toSeries(samples []metricstore.Sample) []SeriesPoint {
	points := make([]SeriesPoint, len(samples))
	for i, s := range samples {
		points[i] = SeriesPoint{TS: s.Timestamp, Value: s.Value}
	}
	return points
}
