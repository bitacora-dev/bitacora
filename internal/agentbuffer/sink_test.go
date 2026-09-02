package agentbuffer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func TestSink_AppendsCollectorItemsAndConvertsToBatch(t *testing.T) {
	buffer, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	defer buffer.Close()

	now := time.Unix(1_700_000_000, 0)
	sink := NewSink("host-a", buffer, WithClock(func() time.Time { return now }))

	labels := schema.Labels{"cpu": "total"}
	sink.Gauge("bitacora_cpu_usage_ratio", 0.42, labels)
	sink.Counter("bitacora_example_ticks_total", 2, nil)
	sink.Event(schema.Event{
		ID: "evt-a", Source: "agent", Type: "agent.started", Severity: schema.SeverityInfo, Title: "started",
	})
	sink.LogLines("journald", []schema.LogLine{{Message: "hello"}})
	sink.Inventory(schema.Inventory{
		Kind: schema.InventoryUser,
		Items: []schema.InventoryItem{
			{ID: "root", Name: "root"},
		},
	})

	labels["cpu"] = "mutated"

	items, err := buffer.oldestItems(10)
	if err != nil {
		t.Fatalf("unexpected error reading items: %v", err)
	}
	batch := ItemsToBatch("host-a", items)

	if batch.HostId != "host-a" || batch.BatchId == "" {
		t.Fatalf("expected host_id and batch_id on batch, got %+v", batch)
	}
	if len(batch.Metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(batch.Metrics))
	}
	if batch.Metrics[0].HostId != "host-a" || batch.Metrics[0].TimestampMs != now.UnixMilli() {
		t.Fatalf("expected sink to stamp metric host/timestamp, got %+v", batch.Metrics[0])
	}
	if batch.Metrics[0].Labels["cpu"] != "total" {
		t.Fatalf("expected labels to be cloned, got %+v", batch.Metrics[0].Labels)
	}
	if len(batch.Events) != 1 || batch.Events[0].HostId != "host-a" || batch.Events[0].TsMs != now.UnixMilli() {
		t.Fatalf("expected event defaults in batch, got %+v", batch.Events)
	}
	if len(batch.LogLines) != 1 || batch.LogLines[0].HostId != "host-a" || batch.LogLines[0].Source != "journald" {
		t.Fatalf("expected log defaults in batch, got %+v", batch.LogLines)
	}
	if len(batch.Inventories) != 1 || batch.Inventories[0].HostId != "host-a" || batch.Inventories[0].ReportedAtMs != now.UnixMilli() {
		t.Fatalf("expected inventory defaults in batch, got %+v", batch.Inventories)
	}
}

func TestSink_RunFlushesAndRetriesWithoutDroppingBufferedItems(t *testing.T) {
	buffer, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening buffer: %v", err)
	}
	defer buffer.Close()

	var logsMu sync.Mutex
	var logs []string
	sink := NewSink("host-a", buffer,
		WithClock(func() time.Time { return time.Unix(1_700_000_000, 0) }),
		WithLogger(func(format string, args ...any) {
			logsMu.Lock()
			defer logsMu.Unlock()
			logs = append(logs, format)
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failOnce := true
	var sent atomic.Int64
	sender := func(ctx context.Context, items []Item) error {
		if failOnce {
			failOnce = false
			return errors.New("network down")
		}
		sent.Add(int64(len(items)))
		return nil
	}
	go sink.Run(ctx, sender, FlushOptions{
		Interval:   time.Hour,
		BatchSize:  10,
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})

	sink.Gauge("bitacora_cpu_usage_ratio", 0.1, nil)

	deadline := time.After(2 * time.Second)
	for {
		if sent.Load() == 1 && buffer.Len() == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected retry to eventually flush item, sent=%d buffered=%d", sent.Load(), buffer.Len())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	logsMu.Lock()
	defer logsMu.Unlock()
	if len(logs) == 0 || !strings.Contains(logs[0], "sending telemetry failed") {
		t.Fatalf("expected send failure to be logged, got %v", logs)
	}
}
