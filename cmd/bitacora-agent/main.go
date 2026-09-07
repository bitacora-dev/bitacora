// bitacora-agent is the per-host daemon (ADR-0002). On startup it probes
// the host's capabilities, sends the resulting manifest to the hub
// (ADR-0004), resolves which collectors can actually run given what's
// available, and starts them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bitacora-dev/bitacora/internal/agentbuffer"
	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/collector/cpu"
	"github.com/bitacora-dev/bitacora/internal/collector/diskarray"
	"github.com/bitacora-dev/bitacora/internal/collector/docker"
	"github.com/bitacora-dev/bitacora/internal/collector/hwidentity"
	"github.com/bitacora-dev/bitacora/internal/collector/journald"
	"github.com/bitacora-dev/bitacora/internal/collector/memory"
	"github.com/bitacora-dev/bitacora/internal/collector/network"
	"github.com/bitacora-dev/bitacora/internal/collector/operations"
	"github.com/bitacora-dev/bitacora/internal/collector/pkgupdates"
	"github.com/bitacora-dev/bitacora/internal/collector/publicsurface"
	"github.com/bitacora-dev/bitacora/internal/collector/shares"
	"github.com/bitacora-dev/bitacora/internal/collector/shareusage"
	"github.com/bitacora-dev/bitacora/internal/collector/ups"
	"github.com/bitacora-dev/bitacora/internal/collector/users"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/transport"
)

// agentVersion is set at build time via -ldflags; "dev" outside a release build.
var agentVersion = "dev"

func main() {
	logger := log.New(os.Stderr, "bitacora-agent: ", log.LstdFlags)
	cfg, err := parseConfig()
	if err != nil {
		logger.Printf("config: %v", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hostID, err := schema.LoadOrCreateHostID(schema.DefaultHostIDPath)
	if err != nil {
		logger.Printf("loading host_id: %v", err)
		os.Exit(1)
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	host := &collector.HostInfo{ID: hostID, Hostname: hostname}

	detectCfg := capabilities.DefaultConfig
	detectCfg.PubliclyExposed = os.Getenv("BITACORA_PUBLIC_EXPOSED") == "1"
	manifest := capabilities.Detect(detectCfg, hostID, hostname, agentVersion, time.Now())
	reportManifest(ctx, manifest, cfg, logger)

	reg := buildRegistry()

	buffer, err := agentbuffer.Open(cfg.spoolDir)
	if err != nil {
		logger.Printf("opening outbound buffer: %v", err)
		os.Exit(1)
	}
	defer buffer.Close()

	regs, disabled := reg.Resolve(ctx, collector.Config{}, host, manifest.Available())
	if cfg.hubURL == "" {
		logger.Printf("sending to no configured hub as %s, %d collectors enabled; telemetry will remain buffered locally", hostID, len(regs))
	} else {
		logger.Printf("sending to %s as %s, %d collectors enabled", cfg.hubURL, hostID, len(regs))
	}

	sink := agentbuffer.NewSink(hostID, buffer, agentbuffer.WithLogger(logger.Printf))
	if cfg.hubURL != "" {
		client := &transport.Client{BaseURL: cfg.hubURL, Token: cfg.token}
		go sink.Run(ctx, agentbuffer.TransportSender(client, hostID), agentbuffer.FlushOptions{})
	} else {
		logger.Printf("hub URL is not configured; telemetry will remain buffered locally")
	}
	collector.EmitDisabledEvents(sink, hostID, disabled, time.Now())

	rt := collector.Runtime{Sink: sink}
	rt.Start(ctx, regs)
	defer rt.Close()

	<-ctx.Done()
}

func buildRegistry() collector.Registry {
	reg := collector.Registry{}
	reg.Register(cpu.New(), 10*time.Second, 5*time.Second)
	reg.Register(memory.New(), 10*time.Second, 5*time.Second)
	reg.Register(network.New(), 30*time.Second, 10*time.Second)
	reg.Register(docker.New(), 30*time.Second, 10*time.Second)
	reg.Register(journald.New(), 10*time.Second, 5*time.Second)
	reg.Register(publicsurface.New(), 5*time.Minute, 30*time.Second)
	reg.Register(shares.New(), 5*time.Minute, 10*time.Second)
	reg.Register(users.New(), 5*time.Minute, 10*time.Second)
	reg.Register(ups.New(), time.Minute, 10*time.Second)
	reg.Register(hwidentity.New(), 5*time.Minute, 10*time.Second)
	reg.Register(diskarray.New(), 5*time.Minute, 10*time.Second)
	// shareusage walks share directories (like `du -sh`), which can take
	// minutes on large media shares — ADR-0016 calls for a low-frequency
	// "once a day" job, not the same cadence as cheap collectors.
	reg.Register(shareusage.New(), 24*time.Hour, time.Minute)
	// pkgupdates' UnRaid-plugin and Docker-image sources each make a real
	// network round-trip per item — a long interval avoids hammering
	// third-party plugin sources and container registries on every cycle,
	// same reasoning as shareusage's cadence above.
	reg.Register(pkgupdates.New(), 6*time.Hour, 2*time.Minute)
	// The operations outbox is producer-owned and append-only; importing it is
	// cheap and gives scheduled backups a real end-to-end path to the hub.
	reg.Register(operations.New(), 15*time.Second, 5*time.Second)
	return reg
}

type config struct {
	hubURL    string
	token     string
	tokenFile string
	spoolDir  string
}

func parseConfig() (config, error) {
	var cfg config
	flag.StringVar(&cfg.hubURL, "hub-url", os.Getenv("BITACORA_HUB_URL"), "hub base URL")
	flag.StringVar(&cfg.tokenFile, "token-file", os.Getenv("BITACORA_TOKEN_FILE"), "path to the ingestion bearer token")
	flag.StringVar(&cfg.spoolDir, "spool-dir", agentbuffer.DefaultOutboundDir, "outbound buffer directory")
	flag.Parse()

	token, err := readToken(cfg.tokenFile, os.Getenv("BITACORA_TOKEN"))
	if err != nil {
		return config{}, err
	}
	cfg.token = token
	if cfg.hubURL != "" && cfg.token == "" {
		return config{}, fmt.Errorf("hub URL requires BITACORA_TOKEN or -token-file")
	}
	return cfg, nil
}

func readToken(path, fallback string) (string, error) {
	if path == "" {
		return strings.TrimSpace(fallback), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading token file %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func reportManifest(ctx context.Context, m capabilities.Manifest, cfg config, logger *log.Logger) {
	if cfg.hubURL == "" {
		return
	}

	client := capabilities.Client{BaseURL: cfg.hubURL, Token: cfg.token}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Send(sendCtx, m); err != nil {
		logger.Printf("sending manifest to hub: %v", err)
	}
}
