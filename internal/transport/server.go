package transport

import (
	"context"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"

	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// DefaultMaxBodyBytes caps a single ingest request body. ADR-0008 targets
// ~1 MB batches; this leaves headroom for the pre-decompression, still-
// compressed size and a safety margin, without being unbounded.
const DefaultMaxBodyBytes = 4 << 20

// BatchReceiver is handed every successfully authenticated, decoded,
// non-duplicate batch. The hub wires this to real storage
// (storage.Relational, metricstore, logstore) — this package doesn't
// assume a specific backend, only the wire protocol and its guarantees.
type BatchReceiver interface {
	ReceiveBatch(ctx context.Context, hostID string, batch *bitacorapb.Batch) error
}

// Server implements POST /v1/ingest (ADR-0008).
type Server struct {
	Tokens       TokenStore
	Idempotency  IdempotencyStore
	Receiver     BatchReceiver
	Manifests    HostManifestRecorder
	Limiter      *PerTokenLimiter // nil disables rate limiting
	MaxBodyBytes int64            // 0 = DefaultMaxBodyBytes
}

// Handler returns the http.Handler serving /v1/ingest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ingest", s.handleIngest)
	mux.HandleFunc("/v1/manifest", s.handleManifest)
	return mux
}

// NewH2CServer returns an *http.Server serving this Server's handler over
// HTTP/2 cleartext (h2c) at addr — the mode used on the loopback/Tailscale
// interface, where TLS is optional (ADR-0008: the network already provides
// confidentiality there).
func (s *Server) NewH2CServer(addr string) *http.Server {
	h2s := &http2.Server{}
	return &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(s.Handler(), h2s),
	}
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, ok := bearerToken(r)
	if !ok {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}

	hostID, ok, err := s.Tokens.Lookup(r.Context(), token)
	if err != nil {
		http.Error(w, "auth error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	if s.Limiter != nil && !s.Limiter.Allow(hostID) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	maxBytes := s.MaxBodyBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		http.Error(w, "reading body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxBytes {
		http.Error(w, "batch too large", http.StatusRequestEntityTooLarge)
		return
	}

	raw, err := decompressZstd(body)
	if err != nil {
		http.Error(w, "decompressing body", http.StatusBadRequest)
		return
	}

	var batch bitacorapb.Batch
	if err := proto.Unmarshal(raw, &batch); err != nil {
		http.Error(w, "invalid protobuf", http.StatusBadRequest)
		return
	}

	if batch.GetHostId() != hostID {
		// ADR-0008: "el hub rechaza un lote cuyo host_id no coincida con
		// el del token. Un agente comprometido no puede falsificar datos
		// de otra máquina."
		http.Error(w, "batch host_id does not match token", http.StatusForbidden)
		return
	}
	if batch.GetBatchId() == "" {
		http.Error(w, "batch_id is required", http.StatusBadRequest)
		return
	}

	duplicate, err := s.Idempotency.MarkSeen(r.Context(), hostID, batch.GetBatchId())
	if err != nil {
		http.Error(w, "idempotency check failed", http.StatusInternalServerError)
		return
	}

	if !duplicate {
		if err := s.Receiver.ReceiveBatch(r.Context(), hostID, &batch); err != nil {
			http.Error(w, "storing batch", http.StatusInternalServerError)
			return
		}
	}

	resp := &bitacorapb.IngestResponse{LastOffset: batch.GetBatchId(), Duplicate: duplicate}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "encoding response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimPrefix(h, prefix), true
}
