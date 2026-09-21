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
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/logstore"
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
	ListEventPage(ctx context.Context, from, to time.Time, hostID, severity, eventType string, limit, offset int) ([]schema.Event, int, error)
}
type JobLister interface {
	ListJobs(ctx context.Context, from, to time.Time, hostID string) ([]schema.Job, error)
}

// JobPoller supplies a consistent job snapshot and cursor-based output pages.
// It is kept separate from JobLister so existing summary-only callers remain
// source-compatible.
type JobPoller interface {
	GetJob(ctx context.Context, hostID, jobID string) (schema.Job, bool, error)
	ListJobOutput(ctx context.Context, hostID, jobID string, afterSequence int64, limit int) ([]schema.JobOutputLine, int64, error)
}

type LogQuerier interface {
	Query(ctx context.Context, query logstore.Query) (logstore.Page, error)
}

// HumanIdentityProvider is the human authentication boundary. Action
// confirmation needs a verified identity, not merely a truthy session bit, so
// audit records can bind the action to a real authenticated subject.
type HumanIdentityProvider interface {
	HasSession(r *http.Request) bool
	Identity(r *http.Request) (hubauth.Identity, bool)
}

type sessionExpiryReporter interface {
	SessionExpired(r *http.Request) bool
}

// ActionConfirmationService is the only route that can create a pending
// package operation. It accepts no observed agent data and exposes no generic
// command mechanism.
type ActionConfirmationService interface {
	Issue(ctx context.Context, input actionconfirm.IssueInput) (actionconfirm.IssuedToken, error)
	Confirm(ctx context.Context, input actionconfirm.ConfirmInput) error
}

// InventoryGetter is the read side of storage.Relational that
// GET /v1/inventory needs (ADR-0015).
type InventoryGetter interface {
	GetInventory(ctx context.Context, hostID string, kind schema.InventoryKind) (schema.Inventory, bool, error)
}

// Server serves the hub's read API and the embedded web UI.
type Server struct {
	Metrics   MetricQuerier
	Events    EventLister
	Jobs      JobLister
	JobPoller JobPoller
	Logs      LogQuerier
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
	// HostRecords stores operator-assigned host names and agent metadata. It
	// is deliberately separate from Hosts, which owns only ingest credentials.
	HostRecords HostRecordStore
	// Humans is the human authentication boundary of ADR-0019. Nil keeps the
	// pre-OIDC behaviour exactly: the UI is served to anyone who reaches the
	// origin and data routes rely on device tokens alone.
	Humans HumanIdentityProvider
	// Actions is nil by default, leaving package actions disabled. Production
	// wires it only when human authentication is configured.
	Actions ActionConfirmationService
}

// Handler returns the http.Handler serving /v1/summary (device-token
// authenticated when Devices is set), host enrollment (always device-token
// authenticated), the pairing bootstrap endpoints, and, if WebUI is set,
// the single-page UI at "/".
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/summary", s.requireDeviceToken(s.handleSummary))
	mux.HandleFunc("/v1/events", s.requireDeviceToken(s.handleEvents))
	mux.HandleFunc("/v1/logs", s.requireDeviceToken(s.handleLogs))
	mux.HandleFunc("/v1/jobs/", s.requireDeviceToken(s.handleJobPoll))
	mux.HandleFunc("/v1/inventory", s.requireDeviceToken(s.handleInventory))
	mux.HandleFunc("/v1/hosts", s.handleHosts)
	mux.HandleFunc("/v1/devices/pair", s.handleDevicePair)
	mux.HandleFunc("/v1/devices/claim", s.handleDeviceClaim)
	mux.HandleFunc("/v1/actions/package-operations", s.handleActionToken)
	mux.HandleFunc("/v1/actions/package-operations/token", s.handleActionToken)
	mux.HandleFunc("/v1/actions/package-operations/confirm", s.handleActionConfirmation)
	if s.WebUI != nil {
		// The UI is the surface ADR-0019 calls out: reaching the origin
		// directly used to be enough to receive it. With OIDC configured it
		// now needs a session of its own.
		mux.Handle("/", s.requireHuman(http.FileServer(http.FS(s.WebUI))))
	}
	return mux
}

// LoginHandler serves only the SPA shell at the public login route. Its
// scripts and styles are embedded in the same binary as the dashboard, so the
// local recovery path has no network or CDN dependency.
func (s *Server) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.WebUI == nil {
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(s.WebUI, "index.html")
		if err != nil {
			http.Error(w, "web interface is unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
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
		// A signed-in person is already authenticated: asking their browser
		// for a device token on top of a verified session would be asking
		// the same question twice.
		if s.Humans != nil && s.Humans.HasSession(r) {
			next(w, r)
			return
		}
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

// requireHuman guards the web UI. Unlike the data routes it redirects instead
// of answering 401: what is being served here is a page, and a browser that
// lands on it should end up at the provider, not at a JSON error.
func (s *Server) requireHuman(next http.Handler) http.Handler {
	if s.Humans == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Humans.HasSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		query := "return_to=" + url.QueryEscape(r.URL.RequestURI())
		if reporter, ok := s.Humans.(sessionExpiryReporter); ok && reporter.SessionExpired(r) {
			query += "&expired=1"
		}
		http.Redirect(w, r, "/auth/login?"+query, http.StatusFound)
	})
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

	hasDeviceToken, err := s.Devices.HasAnyToken(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "checking device tokens")
		return
	}
	if hasDeviceToken {
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

// CPUSeries keeps one logical CPU's samples together. It deliberately does
// not reuse []SeriesPoint: flattening logical CPU series makes a chart join
// unrelated cores into an unreadable line.
type CPUSeries struct {
	CPU    string        `json:"cpu"`
	Points []SeriesPoint `json:"points"`
}

// TemperatureSeries keeps one physical sensor's samples together. A sensor is
// identified by the hwmon chip and sensor labels, so temperatures from
// different devices are never flattened into a misleading single line.
type TemperatureSeries struct {
	Chip   string        `json:"chip"`
	Sensor string        `json:"sensor"`
	Points []SeriesPoint `json:"points"`
}

// PublicSurface groups the internet-facing signals the public_surface
// collector reports (ADR-0004 names them for the VPS case: "intentos SSH
// fallidos, estado de fail2ban, reglas de firewall activas, consumo contra
// la cuota de tráfico de OVH").
//
// Every field is a series, never a scalar, and a signal nobody reported is
// an empty series rather than a zero point. That distinction is the whole
// contract here: the collector only runs on hosts whose operator declared
// them publicly exposed (capabilities.PublicExposed, gated on
// BITACORA_PUBLIC_EXPOSED), so "no samples" is the ordinary state of most
// hosts. Rendering a 0 there would assert "nobody is knocking on this
// door", which is a claim the hub has no evidence for — and the dangerous
// direction to be wrong in.
type PublicSurface struct {
	// SSHFailedLoginsTotal is the collector's raw count of failed-login
	// lines in the current auth log. It is cumulative within one logrotate
	// epoch and drops back when the log rotates, so it is context, not the
	// answer to "am I being attacked right now".
	SSHFailedLoginsTotal []SeriesPoint `json:"ssh_failed_logins_total"`
	// SSHFailedLoginsPerMinute is that counter differentiated: how fast new
	// failed attempts are arriving. This is the series that answers the
	// question, because a standing total of 400 reads identically for a
	// quiet month and for the last twenty minutes.
	SSHFailedLoginsPerMinute []SeriesPoint `json:"ssh_failed_logins_per_minute"`
	Fail2BanJailsTotal       []SeriesPoint `json:"fail2ban_jails_total"`
	Fail2BanBannedTotal      []SeriesPoint `json:"fail2ban_banned_total"`
	FirewallRulesTotal       []SeriesPoint `json:"firewall_rules_total"`
	OVHTrafficUsedRatio      []SeriesPoint `json:"ovh_traffic_used_ratio"`
}

// ContainerSeries keeps one container's samples together, for the same
// reason CPUSeries and TemperatureSeries do: a container is a series, and
// flattening several of them into one list makes a chart join unrelated
// workloads into an unreadable line.
//
// CPUCoresUsed is a rate, not the cumulative counter the collector emits:
// fractions of one logical CPU, so 0.5 means "half a core". It is
// differentiated strictly inside this container's own series — see
// counterRatePoints for why that order is not negotiable.
//
// A container that started, stopped, or was restarted inside the window
// simply has fewer points. There are no zero-filled gaps: a container that
// was not running is absent from the data, which is not the same thing as a
// container that was running and idle.
type ContainerSeries struct {
	ContainerID   string        `json:"container_id"`
	ContainerName string        `json:"container_name"`
	CPUCoresUsed  []SeriesPoint `json:"cpu_cores_used"`
	MemoryBytes   []SeriesPoint `json:"memory_bytes"`
}

// Summary is GET /v1/summary's response: everything the single-page
// timeline view needs to render, in one call (ADR-0014: "el endpoint
// GET /v1/summary?host_id=... debe devolver todo lo necesario para
// pintar la pantalla principal en una sola petición").
type Summary struct {
	HostID                  string              `json:"host_id"`
	GeneratedAt             time.Time           `json:"generated_at"`
	WindowSecs              float64             `json:"window_secs"`
	CPU                     []SeriesPoint       `json:"cpu"`
	CPUCores                []CPUSeries         `json:"cpu_cores"`
	Temperatures            []TemperatureSeries `json:"temperatures"`
	Memory                  []SeriesPoint       `json:"memory"`
	MemoryTotalBytes        []SeriesPoint       `json:"memory_total_bytes"`
	MemoryAvailableBytes    []SeriesPoint       `json:"memory_available_bytes"`
	MemoryUsedBytes         []SeriesPoint       `json:"memory_used_bytes"`
	MemorySwapTotalBytes    []SeriesPoint       `json:"memory_swap_total_bytes"`
	MemorySwapFreeBytes     []SeriesPoint       `json:"memory_swap_free_bytes"`
	NetworkRXBytesPerSecond []SeriesPoint       `json:"network_rx_bytes_per_second"`
	NetworkTXBytesPerSecond []SeriesPoint       `json:"network_tx_bytes_per_second"`
	PublicSurface           PublicSurface       `json:"public_surface"`
	Containers              []ContainerSeries   `json:"containers"`
	Events                  []schema.Event      `json:"events"`
	Jobs                    []schema.Job        `json:"jobs"`
}

// EventHistory is a bounded page of historical events. Events are not pruned
// by this API; retention is a deployment/storage concern and intentionally has
// no configured policy here.
type EventHistory struct {
	HostID string         `json:"host_id"`
	From   time.Time      `json:"from"`
	To     time.Time      `json:"to"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Total  int            `json:"total"`
	Events []schema.Event `json:"events"`
}

const (
	defaultEventHistoryLimit = 100
	maxEventHistoryLimit     = 500
)

// LogHistory is a bounded page of durable log lines. It deliberately exposes
// only flushed blocks: GET never changes ingestion state or forces a flush.
type LogHistory struct {
	HostID  string           `json:"host_id"`
	From    time.Time        `json:"from"`
	To      time.Time        `json:"to"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
	Total   int              `json:"total"`
	Entries []logstore.Entry `json:"entries"`
}

// JobPoll is one polling response. Clients advance after to next_after only
// after processing lines, so reconnects neither lose nor repeat output.
type JobPoll struct {
	Job       schema.Job             `json:"job"`
	Lines     []schema.JobOutputLine `json:"lines"`
	NextAfter int64                  `json:"next_after"`
	Complete  bool                   `json:"complete"`
}

func (s *Server) handleJobPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.JobPoller == nil {
		http.Error(w, "job polling is not configured", http.StatusServiceUnavailable)
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if jobID == "" || strings.Contains(jobID, "/") {
		http.Error(w, "job id is required", http.StatusBadRequest)
		return
	}
	hostID := r.URL.Query().Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id is required", http.StatusBadRequest)
		return
	}
	after := int64(0)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			http.Error(w, "after must be a non-negative integer", http.StatusBadRequest)
			return
		}
	}
	limit := defaultEventHistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxEventHistoryLimit {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
	}
	job, ok, err := s.JobPoller.GetJob(r.Context(), hostID, jobID)
	if err != nil {
		http.Error(w, "querying job", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	lines, next, err := s.JobPoller.ListJobOutput(r.Context(), hostID, jobID, after, limit)
	if err != nil {
		http.Error(w, "querying job output", http.StatusInternalServerError)
		return
	}
	if lines == nil {
		lines = []schema.JobOutputLine{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(JobPoll{Job: job, Lines: lines, NextAfter: next, Complete: job.Status.Terminal()})
}

// handleLogs implements GET /v1/logs over durable log blocks. Ranges are
// explicit and limited to 31 days because text filtering is intentionally
// applied only after metadata has narrowed the compressed-block candidates.
//
// ?block= narrows the page to a single durable block. It is what turns an
// Event's or a Job's log_refs into a reachable page of lines instead of a
// coordinate the caller has no way to resolve.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Logs == nil {
		http.Error(w, "log store is not configured", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	hostID := q.Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id is required", http.StatusBadRequest)
		return
	}
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		http.Error(w, "from must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil || !to.After(from) {
		http.Error(w, "to must be an RFC3339 timestamp after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > logstore.MaxQueryRange {
		http.Error(w, "time range must not exceed 31 days", http.StatusBadRequest)
		return
	}
	limit := defaultEventHistoryLimit
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxEventHistoryLimit {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
			return
		}
	}
	page, err := s.Logs.Query(r.Context(), logstore.Query{HostID: hostID, From: from, To: to, Text: q.Get("text"), Source: q.Get("source"), Unit: q.Get("unit"), BlockID: q.Get("block"), Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, "querying logs", http.StatusInternalServerError)
		return
	}
	if offset > page.Total {
		offset = page.Total
	}
	if page.Entries == nil {
		page.Entries = []logstore.Entry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LogHistory{HostID: hostID, From: from, To: to, Limit: limit, Offset: offset, Total: page.Total, Entries: page.Entries})
}

// handleEvents implements GET /v1/events. Unlike the dashboard summary, its
// time range is explicit so history browsing cannot accidentally turn the
// operational overview into an unbounded query.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	hostID := q.Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id is required", http.StatusBadRequest)
		return
	}
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		http.Error(w, "from must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil || !to.After(from) {
		http.Error(w, "to must be an RFC3339 timestamp after from", http.StatusBadRequest)
		return
	}
	limit := defaultEventHistoryLimit
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxEventHistoryLimit {
			http.Error(w, fmt.Sprintf("limit must be between 1 and %d", maxEventHistoryLimit), http.StatusBadRequest)
			return
		}
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
			return
		}
	}

	events, total, err := s.Events.ListEventPage(r.Context(), from, to, hostID, q.Get("severity"), q.Get("type"), limit, offset)
	if err != nil {
		http.Error(w, "querying events", http.StatusInternalServerError)
		return
	}
	if offset > total {
		offset = total
	}
	if events == nil {
		events = []schema.Event{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(EventHistory{HostID: hostID, From: from, To: to, Limit: limit, Offset: offset, Total: total, Events: events})
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
	totalCPUMatcher := labels.MustNewMatcher(labels.MatchEqual, "cpu", "total")
	coreCPUMatcher := labels.MustNewMatcher(labels.MatchNotEqual, "cpu", "total")
	nonLoopbackInterfaceMatcher := labels.MustNewMatcher(labels.MatchNotEqual, "interface", "lo")

	cpu, err := s.Metrics.Query(r.Context(), "bitacora_cpu_usage_ratio", from, now, hostMatcher, totalCPUMatcher)
	if err != nil {
		http.Error(w, "querying cpu metrics", http.StatusInternalServerError)
		return
	}
	cpuCores, err := s.Metrics.Query(r.Context(), "bitacora_cpu_usage_ratio", from, now, hostMatcher, coreCPUMatcher)
	if err != nil {
		http.Error(w, "querying cpu core metrics", http.StatusInternalServerError)
		return
	}
	temperatures, err := s.Metrics.Query(r.Context(), "bitacora_cpu_temperature_celsius", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying temperature metrics", http.StatusInternalServerError)
		return
	}
	mem, err := s.Metrics.Query(r.Context(), "bitacora_memory_used_ratio", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory metrics", http.StatusInternalServerError)
		return
	}
	memTotal, err := s.Metrics.Query(r.Context(), "bitacora_memory_total_bytes", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory total metrics", http.StatusInternalServerError)
		return
	}
	memAvailable, err := s.Metrics.Query(r.Context(), "bitacora_memory_available_bytes", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory available metrics", http.StatusInternalServerError)
		return
	}
	swapTotal, err := s.Metrics.Query(r.Context(), "bitacora_memory_swap_total_bytes", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory swap total metrics", http.StatusInternalServerError)
		return
	}
	swapFree, err := s.Metrics.Query(r.Context(), "bitacora_memory_swap_free_bytes", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying memory swap free metrics", http.StatusInternalServerError)
		return
	}
	networkRX, err := s.Metrics.Query(r.Context(), "bitacora_net_rx_bytes_total", from, now, hostMatcher, nonLoopbackInterfaceMatcher)
	if err != nil {
		http.Error(w, "querying network receive metrics", http.StatusInternalServerError)
		return
	}
	networkTX, err := s.Metrics.Query(r.Context(), "bitacora_net_tx_bytes_total", from, now, hostMatcher, nonLoopbackInterfaceMatcher)
	if err != nil {
		http.Error(w, "querying network transmit metrics", http.StatusInternalServerError)
		return
	}
	sshFailedLogins, err := s.Metrics.Query(r.Context(), "bitacora_public_ssh_failed_logins_total", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying ssh failed login metrics", http.StatusInternalServerError)
		return
	}
	fail2banJails, err := s.Metrics.Query(r.Context(), "bitacora_public_fail2ban_jails_total", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying fail2ban jail metrics", http.StatusInternalServerError)
		return
	}
	fail2banBanned, err := s.Metrics.Query(r.Context(), "bitacora_public_fail2ban_banned_total", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying fail2ban ban metrics", http.StatusInternalServerError)
		return
	}
	firewallRules, err := s.Metrics.Query(r.Context(), "bitacora_public_firewall_rules_total", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying firewall rule metrics", http.StatusInternalServerError)
		return
	}
	ovhTraffic, err := s.Metrics.Query(r.Context(), "bitacora_public_ovh_traffic_used_ratio", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying ovh traffic quota metrics", http.StatusInternalServerError)
	}

	// The docker collector (ADR-0007) gates itself on the container.cgroupv2
	// capability, so a host without the cgroup v2 unified hierarchy simply
	// reports nothing here. That is absence of data, not zero usage.
	containerCPU, err := s.Metrics.Query(r.Context(), "bitacora_container_cpu_seconds_total", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying container cpu metrics", http.StatusInternalServerError)
		return
	}
	containerMemory, err := s.Metrics.Query(r.Context(), "bitacora_container_memory_bytes", from, now, hostMatcher)
	if err != nil {
		http.Error(w, "querying container memory metrics", http.StatusInternalServerError)
		return
	}
	events, err := s.Events.ListEvents(r.Context(), from, now, hostID)
	if err != nil {
		http.Error(w, "querying events", http.StatusInternalServerError)
		return
	}
	var jobs []schema.Job
	if s.Jobs != nil {
		jobs, err = s.Jobs.ListJobs(r.Context(), from, now, hostID)
		if err != nil {
			http.Error(w, "querying jobs", http.StatusInternalServerError)
			return
		}
	}

	summary := Summary{
		HostID:                  hostID,
		GeneratedAt:             now,
		WindowSecs:              window.Seconds(),
		CPU:                     toSeries(cpu),
		CPUCores:                cpuCoreSeries(cpuCores),
		Temperatures:            temperatureSeries(temperatures),
		Memory:                  toSeries(mem),
		MemoryTotalBytes:        toSeries(memTotal),
		MemoryAvailableBytes:    toSeries(memAvailable),
		MemoryUsedBytes:         memoryUsedSeries(memTotal, memAvailable),
		MemorySwapTotalBytes:    toSeries(swapTotal),
		MemorySwapFreeBytes:     toSeries(swapFree),
		NetworkRXBytesPerSecond: rateSeries(networkRX),
		NetworkTXBytesPerSecond: rateSeries(networkTX),
		PublicSurface: PublicSurface{
			SSHFailedLoginsTotal:     toSeries(sshFailedLogins),
			SSHFailedLoginsPerMinute: perMinuteSeries(sshFailedLogins),
			Fail2BanJailsTotal:       toSeries(fail2banJails),
			Fail2BanBannedTotal:      toSeries(fail2banBanned),
			FirewallRulesTotal:       toSeries(firewallRules),
			OVHTrafficUsedRatio:      toSeries(ovhTraffic),
		},
		Containers: containerSeries(containerCPU, containerMemory),
		Events:     events,
		Jobs:       jobs,
	}
	if summary.Events == nil {
		summary.Events = []schema.Event{}
	}
	if summary.Jobs == nil {
		summary.Jobs = []schema.Job{}
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

func cpuCoreSeries(samples []metricstore.Sample) []CPUSeries {
	byCPU := make(map[string][]metricstore.Sample)
	for _, sample := range samples {
		cpu := sample.Labels["cpu"]
		if cpu == "" {
			continue
		}
		byCPU[cpu] = append(byCPU[cpu], sample)
	}

	cores := make([]CPUSeries, 0, len(byCPU))
	for cpu, points := range byCPU {
		sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
		cores = append(cores, CPUSeries{CPU: cpu, Points: toSeries(points)})
	}
	sort.Slice(cores, func(i, j int) bool {
		left, leftErr := strconv.Atoi(cores[i].CPU)
		right, rightErr := strconv.Atoi(cores[j].CPU)
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return cores[i].CPU < cores[j].CPU
	})
	return cores
}

func temperatureSeries(samples []metricstore.Sample) []TemperatureSeries {
	bySensor := make(map[string][]metricstore.Sample)
	for _, sample := range samples {
		chip, sensor := sample.Labels["chip"], sample.Labels["sensor"]
		if chip == "" || sensor == "" {
			continue
		}
		bySensor[chip+"\x00"+sensor] = append(bySensor[chip+"\x00"+sensor], sample)
	}

	temperatures := make([]TemperatureSeries, 0, len(bySensor))
	for key, points := range bySensor {
		chip, sensor, _ := strings.Cut(key, "\x00")
		sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
		temperatures = append(temperatures, TemperatureSeries{Chip: chip, Sensor: sensor, Points: toSeries(points)})
	}
	sort.Slice(temperatures, func(i, j int) bool {
		if temperatures[i].Chip == temperatures[j].Chip {
			return temperatures[i].Sensor < temperatures[j].Sensor
		}
		return temperatures[i].Chip < temperatures[j].Chip
	})
	return temperatures
}

// rateSeries turns cumulative counter samples (e.g. the network collector's
// bitacora_net_{rx,tx}_bytes_total, which retain an interface label in
// storage) into one host-level per-second rate point per timestamp.
//
// The order of operations matters and is the entire point of this function:
// each label-set's series (e.g. one series per interface) is differentiated
// on its own, in timestamp order, and only the resulting per-series rates
// are summed together by timestamp afterwards. Summing the raw cumulative
// counters across interfaces FIRST and differentiating the sum afterwards
// would be wrong: an interface appearing or disappearing mid-window (a NIC
// coming up, a VPN interface going away) would produce a huge, spurious
// jump in the summed counter, which differentiation would then report as
// an equally spurious rate spike. Differentiating per-series before summing
// avoids that entirely — a future "simplification" that flattens this back
// into one sum-then-diff pass would reintroduce exactly that bug.
//
// Summing by exact timestamp relies on an agent-side invariant: every
// metric of one collection cycle carries the same instant
// (collector.CycleSink). When it didn't, each interface landed on its own
// millisecond and this function returned one point per interface per cycle
// — each holding a single interface's rate — instead of one point holding
// their sum, which read as loose dots on the chart and as the idlest
// interface's 0 B/s on the current-value readout.
func rateSeries(samples []metricstore.Sample) []SeriesPoint {
	if len(samples) == 0 {
		return []SeriesPoint{}
	}

	bySeries := make(map[string][]metricstore.Sample)
	for _, sample := range samples {
		key := labelSetKey(sample.Labels)
		bySeries[key] = append(bySeries[key], sample)
	}

	rateByTS := make(map[time.Time]float64)
	for _, series := range bySeries {
		for _, point := range counterRatePoints(series) {
			rateByTS[point.TS] += point.Value
		}
	}

	timestamps := make([]time.Time, 0, len(rateByTS))
	for timestamp := range rateByTS {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i].Before(timestamps[j]) })

	points := make([]SeriesPoint, 0, len(timestamps))
	for _, timestamp := range timestamps {
		points = append(points, SeriesPoint{TS: timestamp, Value: rateByTS[timestamp]})
	}
	return points
}

// perMinuteSeries turns a cumulative counter into new-events-per-minute.
//
// It reuses rateSeries deliberately, for its two guarantees rather than for
// brevity: the first sample of a series yields no point at all (never a
// zero one), and a counter that goes backwards is skipped instead of
// reported as a negative rate. Both matter here. The public-surface
// collector re-counts matching lines in the *current* auth log on every
// cycle, so logrotate drops the count back to near zero — a reset the
// dashboard must not draw as "the attack stopped".
//
// Per minute rather than rateSeries's per second because the collector runs
// on a 5-minute cadence: a brute-force run of 100 attempts between two
// cycles reads as "20 attempts/min", not as "0.3/s".
func perMinuteSeries(samples []metricstore.Sample) []SeriesPoint {
	points := rateSeries(samples)
	for i := range points {
		points[i].Value *= 60
	}
	return points
}

// counterRatePoints differentiates ONE series of cumulative counter samples
// into per-second rate points. It is the single place that decides what a
// valid interval is, so every counter this API exposes — host network bytes,
// per-container CPU seconds — answers that question the same way.
//
// It takes an already-grouped series on purpose. Differentiating each series
// on its own and only then combining the results is the whole discipline:
// combining the raw cumulative counters first and differentiating the sum
// afterwards reports every series that appears or disappears mid-window as a
// spike that never happened. For containers that is not an edge case — on a
// Dokploy host containers start and stop all day, and each one starts its
// cgroup v2 cpu.stat back at zero.
func counterRatePoints(series []metricstore.Sample) []SeriesPoint {
	sorted := make([]metricstore.Sample, len(series))
	copy(sorted, series)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp.Before(sorted[j].Timestamp) })

	points := make([]SeriesPoint, 0, len(sorted))
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		elapsed := cur.Timestamp.Sub(prev.Timestamp).Seconds()
		if elapsed <= 0 {
			continue // out-of-order or duplicate timestamp — not a valid interval
		}
		if cur.Value < prev.Value {
			continue // counter reset (reboot, interface reset, container restart) — not an error, just not a valid rate point
		}
		points = append(points, SeriesPoint{TS: cur.Timestamp, Value: (cur.Value - prev.Value) / elapsed})
	}
	return points
}

// containerSeries turns the docker collector's two labelled metrics into one
// entry per container, never into one flattened list.
//
// Grouping is by container_id, the stable identity (truncated to 12
// characters by the collector, per ADR-0006). container_name is display
// metadata and can change within a window: the collector falls back to the
// truncated id while docker-socket-proxy is unavailable (ADR-0005's degraded
// mode), so the most recent name wins rather than the first one seen.
//
// CPU is differentiated per container before anything else happens to it.
// Memory is a gauge and is passed through in timestamp order.
func containerSeries(cpu, memory []metricstore.Sample) []ContainerSeries {
	type bucket struct {
		name   string
		nameTS time.Time
		cpu    []metricstore.Sample
		memory []metricstore.Sample
	}

	buckets := make(map[string]*bucket)
	observe := func(sample metricstore.Sample) *bucket {
		id := sample.Labels["container_id"]
		if id == "" {
			return nil // not a per-container sample; nothing to attribute it to
		}
		current, ok := buckets[id]
		if !ok {
			current = &bucket{}
			buckets[id] = current
		}
		if name := sample.Labels["container_name"]; name != "" && !sample.Timestamp.Before(current.nameTS) {
			current.name, current.nameTS = name, sample.Timestamp
		}
		return current
	}

	for _, sample := range cpu {
		if current := observe(sample); current != nil {
			current.cpu = append(current.cpu, sample)
		}
	}
	for _, sample := range memory {
		if current := observe(sample); current != nil {
			current.memory = append(current.memory, sample)
		}
	}

	containers := make([]ContainerSeries, 0, len(buckets))
	for id, current := range buckets {
		name := current.name
		if name == "" {
			name = id // schema validation requires a name, but never render a blank row
		}
		sort.Slice(current.memory, func(i, j int) bool {
			return current.memory[i].Timestamp.Before(current.memory[j].Timestamp)
		})
		containers = append(containers, ContainerSeries{
			ContainerID:   id,
			ContainerName: name,
			CPUCoresUsed:  counterRatePoints(current.cpu),
			MemoryBytes:   toSeries(current.memory),
		})
	}

	// Sorted by name so the panel keeps a stable order between polls even as
	// containers come and go; the id breaks ties between replicas sharing a name.
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].ContainerName == containers[j].ContainerName {
			return containers[i].ContainerID < containers[j].ContainerID
		}
		return containers[i].ContainerName < containers[j].ContainerName
	})
	return containers
}

// labelSetKey builds a stable, order-independent identity for a sample's
// full label set, so rateSeries can group samples into the distinct series
// (e.g. one per interface) they came from before differentiating.
func labelSetKey(set map[string]string) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(set[k])
		b.WriteByte(';')
	}
	return b.String()
}

func memoryUsedSeries(total, available []metricstore.Sample) []SeriesPoint {
	if len(total) == 0 || len(available) == 0 {
		return []SeriesPoint{}
	}
	points := make([]SeriesPoint, 0, len(available))
	totalByTS := make(map[time.Time]float64, len(total))
	for _, sample := range total {
		totalByTS[sample.Timestamp] = sample.Value
	}
	latestTotal := total[len(total)-1].Value
	for _, sample := range available {
		totalValue, ok := totalByTS[sample.Timestamp]
		if !ok {
			totalValue = latestTotal
		}
		used := totalValue - sample.Value
		if used < 0 {
			used = 0
		}
		points = append(points, SeriesPoint{TS: sample.Timestamp, Value: used})
	}
	return points
}
