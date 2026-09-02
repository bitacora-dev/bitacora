package agentbuffer

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// TransportSender adapts a transport.Client into the Sender Backfill
// needs: it packs a batch of Items into a bitacorapb.Batch (with a fresh
// ULID batch_id, per ADR-0008) and sends it. This is the reference
// wiring a real agent uses; Backfill itself doesn't know transport
// exists, so agentbuffer stays testable without a network stack.
func TransportSender(client *transport.Client, hostID string) Sender {
	return func(ctx context.Context, items []Item) error {
		batch := ItemsToBatch(hostID, items)
		_, err := client.Send(ctx, batch)
		if err != nil {
			return fmt.Errorf("sending batch of %d item(s): %w", len(items), err)
		}
		return nil
	}
}

func ItemsToBatch(hostID string, items []Item) *bitacorapb.Batch {
	batch := &bitacorapb.Batch{
		BatchId: ulid.Make().String(),
		HostId:  hostID,
	}
	for _, it := range items {
		switch {
		case it.Metric != nil:
			batch.Metrics = append(batch.Metrics, metricToProto(it.Metric))
		case it.Event != nil:
			batch.Events = append(batch.Events, eventToProto(it.Event))
		case it.LogLine != nil:
			batch.LogLines = append(batch.LogLines, logLineToProto(it.LogLine))
		case it.Inventory != nil:
			batch.Inventories = append(batch.Inventories, inventoryToProto(it.Inventory))
		}
	}
	return batch
}

func metricToProto(m *schema.Metric) *bitacorapb.Metric {
	labels := make(map[string]string, len(m.Labels))
	for k, v := range m.Labels {
		labels[k] = v
	}
	return &bitacorapb.Metric{
		Name:        m.Name,
		HostId:      m.HostID,
		Labels:      labels,
		Value:       m.Value,
		TimestampMs: m.Timestamp.UnixMilli(),
	}
}

func eventToProto(e *schema.Event) *bitacorapb.Event {
	attrs := make(map[string]string, len(e.Attrs))
	for k, v := range e.Attrs {
		attrs[k] = v
	}

	var logRefs []*bitacorapb.LogRef
	for _, r := range e.LogRefs {
		logRefs = append(logRefs, &bitacorapb.LogRef{BlockId: r.BlockID, Line: int32(r.Line)})
	}

	pb := &bitacorapb.Event{
		Id:          e.ID,
		TsMs:        e.TS.UnixMilli(),
		HostId:      e.HostID,
		Source:      e.Source,
		Type:        e.Type,
		Severity:    string(e.Severity),
		Title:       e.Title,
		Attrs:       attrs,
		Fingerprint: e.Fingerprint,
		LogRefs:     logRefs,
		Schema:      int32(e.Schema),
		Subject: &bitacorapb.EventSubject{
			Kind: e.Subject.Kind,
			Name: e.Subject.Name,
			Pid:  int32(e.Subject.PID),
		},
	}
	if !e.TSReceived.IsZero() {
		pb.TsReceivedMs = e.TSReceived.UnixMilli()
	}
	return pb
}

func inventoryToProto(inv *schema.Inventory) *bitacorapb.Inventory {
	items := make([]*bitacorapb.InventoryItem, 0, len(inv.Items))
	for _, it := range inv.Items {
		attrs := make(map[string]string, len(it.Attrs))
		for k, v := range it.Attrs {
			attrs[k] = v
		}
		items = append(items, &bitacorapb.InventoryItem{Id: it.ID, Name: it.Name, Attrs: attrs})
	}
	return &bitacorapb.Inventory{
		HostId:       inv.HostID,
		Kind:         string(inv.Kind),
		ReportedAtMs: inv.ReportedAt.UnixMilli(),
		Schema:       int32(inv.Schema),
		Items:        items,
	}
}

func logLineToProto(l *schema.LogLine) *bitacorapb.LogLine {
	return &bitacorapb.LogLine{
		TsMs:            l.TS.UnixMilli(),
		HostId:          l.HostID,
		Source:          l.Source,
		Stream:          l.Stream,
		UnitOrContainer: l.UnitOrContainer,
		Level:           l.Level,
		Pid:             int32(l.PID),
		Message:         l.Message,
	}
}
