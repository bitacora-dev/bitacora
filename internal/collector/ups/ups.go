// Package ups implements the SAI/UPS inventory collector (ADR-0015,
// ADR-0016) via NUT (Network UPS Tools). NUT's protocol is a simple,
// line-based ASCII protocol over TCP (upsd, default port 3493) — no
// privilege needed, just a socket. apcupsd support (ADR-0016 names it as
// the alternative) isn't implemented here: its NIS protocol uses a
// different, length-prefixed binary framing this package doesn't speak
// yet — a documented followup, not a silent gap.
package ups

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultAddr    = "127.0.0.1:3493"
	dialTimeout    = 3 * time.Second
	requestTimeout = 5 * time.Second
)

// Collector emits an Inventory of kind ups (ADR-0015).
type Collector struct {
	addr   string
	hostID string
	dial   func(ctx context.Context, addr string) (net.Conn, error)
}

// New returns a collector with production defaults.
func New() *Collector {
	return &Collector{
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "tcp", addr)
		},
	}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "ups" }

// Requires implements collector.Collector.
func (c *Collector) Requires() []collector.Capability {
	return []collector.Capability{capabilities.PowerUPS}
}

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.addr = defaultAddr
	if v, ok := cfg["nut_addr"].(string); ok && v != "" {
		c.addr = v
	}
	if host != nil {
		c.hostID = host.ID
	}
	return nil
}

// Collect implements collector.Collector. A NUT server that's
// unreachable (not installed, not running) yields an empty snapshot, not
// an error — same degrade-gracefully posture as every other ADR-0015
// addition.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	items, _ := c.readNUT(ctx)
	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryUPS,
		ReportedAt: time.Now().UTC(),
		Schema:     schema.CurrentSchemaVersion,
		Items:      items,
	})
	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func (c *Collector) readNUT(ctx context.Context) ([]schema.InventoryItem, error) {
	conn, err := c.dial(ctx, c.addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))

	// One shared reader for the whole connection: NUT is a stateful,
	// pipelined text protocol over a single socket, and wrapping it in a
	// fresh bufio.Reader per command would silently discard whatever the
	// previous reader had already buffered past the response it parsed.
	reader := bufio.NewReader(conn)

	names, err := listUPSNames(conn, reader)
	if err != nil {
		return nil, err
	}

	var items []schema.InventoryItem
	for _, name := range names {
		vars, err := listUPSVars(conn, reader, name)
		if err != nil {
			continue // one UPS failing to respond shouldn't hide the others
		}
		items = append(items, buildItem(name, vars))
	}
	return items, nil
}

// listUPSNames sends NUT's "LIST UPS" and parses the "UPS <name>
// "<description>"" lines between BEGIN/END markers.
func listUPSNames(conn net.Conn, reader *bufio.Reader) ([]string, error) {
	if _, err := fmt.Fprint(conn, "LIST UPS\n"); err != nil {
		return nil, err
	}

	var names []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "END LIST UPS") {
			break
		}
		if !strings.HasPrefix(line, "UPS ") {
			continue // BEGIN LIST UPS or an unexpected line
		}
		fields := strings.SplitN(line, " ", 3)
		if len(fields) >= 2 {
			names = append(names, fields[1])
		}
	}
	return names, nil
}

// listUPSVars sends "LIST VAR <name>" and parses the "VAR <name>
// <varname> "<value>"" lines into a map.
func listUPSVars(conn net.Conn, reader *bufio.Reader, name string) (map[string]string, error) {
	if _, err := fmt.Fprintf(conn, "LIST VAR %s\n", name); err != nil {
		return nil, err
	}

	vars := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "END LIST VAR") {
			break
		}
		if !strings.HasPrefix(line, "VAR ") {
			continue
		}
		// VAR <upsname> <varname> "<value>"
		fields := strings.SplitN(line, " ", 4)
		if len(fields) != 4 {
			continue
		}
		varName := fields[2]
		value := strings.Trim(fields[3], `"`)
		vars[varName] = value
	}
	return vars, nil
}

func buildItem(name string, vars map[string]string) schema.InventoryItem {
	attrs := schema.Labels{}
	if status, ok := vars["ups.status"]; ok {
		attrs["status"] = status
		attrs["on_battery"] = strconv.FormatBool(strings.Contains(status, "OB"))
	}
	if charge, ok := vars["battery.charge"]; ok {
		attrs["battery_charge_pct"] = charge
	}
	if runtime, ok := vars["battery.runtime"]; ok {
		attrs["runtime_seconds"] = runtime
	}
	if model, ok := vars["ups.model"]; ok {
		attrs["model"] = model
	}
	return schema.InventoryItem{ID: name, Name: name, Attrs: attrs}
}
