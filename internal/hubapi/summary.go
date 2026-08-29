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

// Server serves the hub's read API and the embedded web UI.
type Server struct {
	Metrics MetricQuerier
	Events  EventLister
	// WebUI is the built frontend (ADR-0001: React+Vite+Tailwind+uPlot,
	// embedded via go:embed). Nil disables serving it — useful for
	// testing the API in isolation.
	WebUI fs.FS
}

// Handler returns the http.Handler serving both /v1/summary and, if
// WebUI is set, the single-page UI at "/".
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/summary", s.handleSummary)
	if s.WebUI != nil {
		mux.Handle("/", http.FileServer(http.FS(s.WebUI)))
	}
	return mux
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

func toSeries(samples []metricstore.Sample) []SeriesPoint {
	points := make([]SeriesPoint, len(samples))
	for i, s := range samples {
		points[i] = SeriesPoint{TS: s.Timestamp, Value: s.Value}
	}
	return points
}
