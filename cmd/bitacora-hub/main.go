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
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/bitacora-dev/bitacora/internal/actionconfirm"
	"github.com/bitacora-dev/bitacora/internal/hubapi"
	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/hubpipeline"
	"github.com/bitacora-dev/bitacora/internal/ingestreceiver"
	"github.com/bitacora-dev/bitacora/internal/logstore"
	"github.com/bitacora-dev/bitacora/internal/metricstore"
	"github.com/bitacora-dev/bitacora/internal/storage"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/internal/transport/sqlitetokenstore"
	"github.com/bitacora-dev/bitacora/internal/websecurity"
	"github.com/bitacora-dev/bitacora/internal/webui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		if err := runLocalAuthCommand(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	addr := flag.String("addr", "127.0.0.1:8081", "listen address (ADR-0002: agent talks to hub here by default)")
	dataDir := flag.String("data-dir", "/var/lib/bitacora", "base directory for hub data")
	extractionRulesDir := flag.String("extraction-rules-dir", hubpipeline.DefaultExtractionRulesDir, "operator extraction rules directory")
	alertRulesDir := flag.String("alert-rules-dir", hubpipeline.DefaultAlertRulesDir, "operator event alert rules directory")
	notificationsPath := flag.String("notifications", hubpipeline.DefaultNotificationsPath, "notification destinations YAML file")
	hubURL := flag.String("hub-url", "", "public hub URL used in notification deep links")
	addToken := flag.String("add-token", "", "register an ingest token and exit, without starting the server: <host_id>:<token-en-texto-plano>")
	setupTokenTTL := flag.Duration("setup-token-ttl", hubauth.DefaultSetupTokenTTL, "lifetime of the install token printed on a start without any login source, at most 24h (ADR-0025)")
	flag.Parse()

	if err := hubauth.ValidateSetupTokenTTL(*setupTokenTTL); err != nil {
		log.Fatal(fmt.Errorf("-setup-token-ttl: %w", err))
	}
	if *addToken != "" {
		if err := runAddToken(*dataDir, *addToken); err != nil {
			log.Fatal(err)
		}
		return
	}

	localConfig, err := hubauth.LocalConfigFromEnv()
	if err != nil {
		log.Fatal(fmt.Errorf("reading local authentication configuration: %w", err))
	}
	localAuth, err := hubauth.LoadLocalStore(localConfig)
	if err != nil {
		log.Fatal(fmt.Errorf("opening local authentication: %w", err))
	}
	if localAuth == nil {
		// ADR-0025: an uninitialized store is still a store. The web setup
		// writes the credential here, at the configured paths.
		localAuth = hubauth.NewLocalStore(localConfig.StatePath(), localConfig.KeyStatePath())
	}
	h, err := newHubWithLocalAuth(*dataDir, localAuth, hubpipeline.Config{
		ExtractionRulesDir: *extractionRulesDir,
		AlertRulesDir:      *alertRulesDir,
		NotificationsPath:  *notificationsPath,
		HubURL:             *hubURL,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()
	if err := announceSetup(h.auth, localAuth, *setupTokenTTL, os.Stderr); err != nil {
		log.Fatal(err)
	}
	securityConfig, err := websecurity.ConfigFromEnv()
	if err != nil {
		log.Fatal(fmt.Errorf("reading trusted proxy configuration: %w", err))
	}
	hasSession := h.auth.HasSession

	log.Printf("bitacora-hub listening on %s (data: %s)", *addr, *dataDir)
	httpSrv := &http.Server{
		Addr:    *addr,
		Handler: h2c.NewHandler(websecurity.Middleware(h.handler, securityConfig, hasSession), &http2.Server{}),
		// Header and idle deadlines bound slowloris and abandoned keep-alive
		// connections. ReadTimeout and WriteTimeout intentionally stay unset:
		// they cover an entire request/response and would terminate legitimate
		// long ingest bodies or future streaming responses under h2c.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
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
	auth    *hubauth.Authenticator
	closers []io.Closer
}

func (h *hub) Close() {
	for _, c := range h.closers {
		if err := c.Close(); err != nil {
			log.Printf("closing store: %v", err)
		}
	}
}

// newHubWithLocalAuth wires the read API and web UI (hubapi.Server) and the
// real /v1/ingest endpoint (transport.Server, per ADR-0008) against storage
// rooted at dataDir, merged into a single handler served by one listener.
// localAuth is required, initialized or not: production passes the configured
// credential paths and tests a private temporary credential, so neither ever
// reads or writes /etc by accident.
func newHubWithLocalAuth(dataDir string, localAuth *hubauth.LocalStore, pipelineConfig ...hubpipeline.Config) (*hub, error) {
	if localAuth == nil {
		return nil, fmt.Errorf("configuring human authentication: a local credential store is required")
	}
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
	config := hubpipeline.Config{}
	if len(pipelineConfig) > 0 {
		config = pipelineConfig[0]
	}
	processor, err := hubpipeline.New(relStore, config)
	if err != nil {
		relStore.Close()
		metricsStore.Close()
		return nil, fmt.Errorf("opening log pipeline: %w", err)
	}

	tokenStore, err := sqlitetokenstore.New(tokenStorePath(dataDir))
	if err != nil {
		relStore.Close()
		metricsStore.Close()
		return nil, fmt.Errorf("opening token store: %w", err)
	}

	// Device-token hashes share the SQLite file with ingest credentials, but
	// use their own table and methods because they are never bound to host_id.
	devices := hubapi.NewDeviceTokenStore(tokenStore)
	readSrv := &hubapi.Server{
		Metrics:     metricsStore,
		Events:      relStore,
		Jobs:        relStore,
		JobPoller:   relStore,
		Logs:        logStore,
		Inventories: relStore,
		WebUI:       webui.FS(),
		Devices:     devices,
		// Same store -add-token writes to: enrolling a host from the web
		// UI (POST /v1/hosts) and from the CLI must produce exactly the
		// same persisted Argon2id hash, not two parallel registries.
		Hosts:       tokenStore,
		HostRecords: relStore,
		// The lifecycle side of that same store: revoking, rotating and
		// listing credentials, and the one-time enrollment codes that let a
		// new machine fetch its own token instead of having it copied by
		// hand (ADR-0008).
		Tokens:      tokenStore,
		Enrollments: tokenStore,
	}

	ingestSrv := &transport.Server{
		Tokens:      tokenStore,
		Idempotency: transport.NewMemoryIdempotencyStore(),
		Receiver: ingestreceiver.New(
			metricsStore,
			relStore,
			logStore,
			ingestreceiver.WithLogProcessor(processor),
			ingestreceiver.WithInventoryUpserter(relStore),
			ingestreceiver.WithJobInserter(relStore),
		),
		Manifests: relStore,
	}

	// ADR-0019/0023/0025: the human boundary always exists and fails closed.
	// With neither OIDC nor an enabled local credential, the hub serves only
	// the setup screen — never its dashboard to whoever reaches the origin
	// first. Failing to reach a configured issuer stops the hub on purpose,
	// for the same reason.
	auth, err := hubauth.NewBoundary(context.Background(), hubauth.ConfigFromEnv(), localAuth)
	if err != nil {
		relStore.Close()
		metricsStore.Close()
		tokenStore.Close()
		return nil, fmt.Errorf("configuring human authentication: %w", err)
	}
	readSrv.Humans = auth
	// ADR-0022 actions need a verified human identity, which only a session
	// provides; in setup mode the boundary withholds the route entirely. The
	// persistent store is also the ingest pull source, so confirmed orders
	// travel only in the established response channel.
	actions := actionconfirm.NewStore(relStore)
	readSrv.Actions = actions
	ingestSrv.Orders = actions

	mux := http.NewServeMux()
	// Registered as an exact path, so it wins over "/" in the ServeMux and
	// the agent-facing ingest never passes through the human boundary:
	// requiring a browser session here would silence every agent at once.
	mux.Handle("/v1/ingest", ingestSrv.Handler())
	// These exact routes win over /auth/: they are the unguarded, embedded
	// login and setup shells. API endpoints remain owned by the
	// authenticator below.
	mux.Handle("/auth/login", readSrv.LoginHandler())
	mux.Handle("/auth/setup", readSrv.LoginHandler())
	// The shells' own committed assets must be reachable before a session
	// exists. They are embedded files, never third-party URLs.
	mux.Handle("/assets/", http.FileServer(http.FS(webui.FS())))
	mux.Handle("/bitacora-logo.png", http.FileServer(http.FS(webui.FS())))
	mux.Handle("/auth/", auth.Handler())
	mux.Handle("/", readSrv.Handler())

	return &hub{
		handler: mux,
		tokens:  tokenStore,
		devices: devices,
		auth:    auth,
		closers: []io.Closer{relStore, metricsStore, tokenStore},
	}, nil
}
