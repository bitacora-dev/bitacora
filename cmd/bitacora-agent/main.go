// bitacora-agent is the per-host daemon (ADR-0002). On startup it probes
// the host's capabilities, sends the resulting manifest to the hub
// (ADR-0004), resolves which collectors can actually run given what's
// available, and starts them.
//
// The Sink implementation here is a stdout placeholder: the real transport
// to the hub (ADR-0008: HTTP/2, Protobuf, zstd, local WAL) isn't wired yet.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/collector/example"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// agentVersion is set at build time via -ldflags; "dev" outside a release build.
var agentVersion = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hostID, err := schema.LoadOrCreateHostID(schema.DefaultHostIDPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bitacora-agent: loading host_id: %v\n", err)
		os.Exit(1)
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	host := &collector.HostInfo{ID: hostID, Hostname: hostname}

	manifest := capabilities.Detect(capabilities.DefaultConfig, hostID, hostname, agentVersion, time.Now())
	reportManifest(ctx, manifest)

	reg := collector.Registry{}
	reg.Register(example.New(), 10*time.Second, 5*time.Second)

	sink := stdoutSink{}
	regs, disabled := reg.Resolve(ctx, collector.Config{}, host, manifest.Available())
	collector.EmitDisabledEvents(sink, disabled, time.Now())

	rt := collector.Runtime{Sink: sink}
	rt.Start(ctx, regs)
	defer rt.Close()

	<-ctx.Done()
}

// reportManifest sends the manifest to the hub if BITACORA_HUB_URL is set,
// and always prints it: a hub that's down (or not configured yet, as in
// this early stage of the project) must never stop the agent from
// collecting.
func reportManifest(ctx context.Context, m capabilities.Manifest) {
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		fmt.Println(string(data))
	}

	hubURL := os.Getenv("BITACORA_HUB_URL")
	if hubURL == "" {
		return
	}

	client := capabilities.Client{BaseURL: hubURL, Token: os.Getenv("BITACORA_TOKEN")}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Send(sendCtx, m); err != nil {
		fmt.Fprintf(os.Stderr, "bitacora-agent: sending manifest to hub: %v\n", err)
	}
}

// stdoutSink is a placeholder Sink until the real transport (ADR-0008) is
// implemented: it makes the agent runnable end-to-end without pretending to
// talk to a hub that doesn't exist yet.
type stdoutSink struct{}

func (stdoutSink) Gauge(name string, value float64, labels collector.Labels) {
	fmt.Printf("gauge %s=%v %v\n", name, value, labels)
}

func (stdoutSink) Counter(name string, value float64, labels collector.Labels) {
	fmt.Printf("counter %s=%v %v\n", name, value, labels)
}

func (stdoutSink) Event(e collector.Event) {
	fmt.Printf("event %s [%s] %s %v\n", e.Type, e.Level, e.Message, e.Attrs)
}

func (stdoutSink) LogLines(source string, lines []collector.LogLine) {
	for _, l := range lines {
		fmt.Printf("log %s: %s\n", source, l.Line)
	}
}
