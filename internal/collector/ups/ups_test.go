package ups

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type recordingSink struct {
	inventories []schema.Inventory
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}
func (s *recordingSink) Inventory(inv collector.Inventory) {
	s.inventories = append(s.inventories, inv)
}

// startFakeNUTServer runs a minimal real NUT (upsd) protocol server over a
// real TCP listener, exactly matching the request/response shape a
// production NUT server sends for LIST UPS / LIST VAR.
func startFakeNUTServer(t *testing.T, upsName string, vars map[string]string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)

			switch {
			case line == "LIST UPS":
				conn.Write([]byte("BEGIN LIST UPS\n"))
				conn.Write([]byte("UPS " + upsName + " \"Test UPS\"\n"))
				conn.Write([]byte("END LIST UPS\n"))
			case strings.HasPrefix(line, "LIST VAR "):
				name := strings.TrimPrefix(line, "LIST VAR ")
				conn.Write([]byte("BEGIN LIST VAR " + name + "\n"))
				for k, v := range vars {
					conn.Write([]byte("VAR " + name + " " + k + " \"" + v + "\"\n"))
				}
				conn.Write([]byte("END LIST VAR " + name + "\n"))
			default:
				return
			}
		}
	}()

	return ln.Addr().String()
}

func TestCollector_ReadsUPSStatusFromRealNUTServer(t *testing.T) {
	addr := startFakeNUTServer(t, "apc", map[string]string{
		"ups.status":      "OL",
		"battery.charge":  "100",
		"battery.runtime": "1800",
		"ups.model":       "Smart-UPS 1500",
	})

	c := New()
	if err := c.Init(context.Background(), collector.Config{"nut_addr": addr}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 || sink.inventories[0].Kind != schema.InventoryUPS {
		t.Fatalf("unexpected inventory: %+v", sink.inventories)
	}
	items := sink.inventories[0].Items
	if len(items) != 1 || items[0].ID != "apc" {
		t.Fatalf("expected 1 UPS named apc, got %+v", items)
	}

	attrs := items[0].Attrs
	if attrs["status"] != "OL" || attrs["on_battery"] != "false" {
		t.Fatalf("expected online status, got %+v", attrs)
	}
	if attrs["battery_charge_pct"] != "100" || attrs["runtime_seconds"] != "1800" {
		t.Fatalf("unexpected battery attrs: %+v", attrs)
	}
	if attrs["model"] != "Smart-UPS 1500" {
		t.Fatalf("expected model to be captured, got %+v", attrs)
	}
}

func TestCollector_OnBatteryStatusIsDetected(t *testing.T) {
	addr := startFakeNUTServer(t, "apc", map[string]string{
		"ups.status": "OB LB",
	})

	c := New()
	if err := c.Init(context.Background(), collector.Config{"nut_addr": addr}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sink.inventories[0].Items[0].Attrs["on_battery"] != "true" {
		t.Fatalf("expected on_battery=true for status OB LB, got %+v", sink.inventories[0].Items[0].Attrs)
	}
}

func TestCollector_UnreachableServerYieldsEmptySnapshotNotError(t *testing.T) {
	c := New()
	// Nothing listens on this address.
	if err := c.Init(context.Background(), collector.Config{"nut_addr": "127.0.0.1:1"}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 1 || len(sink.inventories[0].Items) != 0 {
		t.Fatalf("expected an empty (not missing) snapshot, got %+v", sink.inventories)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestCollector_RequiresPowerUPSCapability(t *testing.T) {
	c := New()
	req := c.Requires()
	if len(req) != 1 {
		t.Fatalf("expected exactly power.ups as a requirement, got %+v", req)
	}
}
