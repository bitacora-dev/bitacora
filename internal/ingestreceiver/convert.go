package ingestreceiver

import (
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

// protoToMetric, protoToEvent, protoToLogLine and protoToInventory are the mirror image of
// agentbuffer.metricToProto/eventToProto/logLineToProto: those pack
// schema.* into bitacorapb.* on the agent's way out, these unpack
// bitacorapb.* back into schema.* on the hub's way in.

func protoToMetric(m *bitacorapb.Metric) schema.Metric {
	var labels schema.Labels
	if len(m.GetLabels()) > 0 {
		labels = make(schema.Labels, len(m.GetLabels()))
		for k, v := range m.GetLabels() {
			labels[k] = v
		}
	}
	return schema.Metric{
		Name:      m.GetName(),
		HostID:    m.GetHostId(),
		Labels:    labels,
		Value:     m.GetValue(),
		Timestamp: time.UnixMilli(m.GetTimestampMs()).UTC(),
	}
}

func protoToEvent(e *bitacorapb.Event) schema.Event {
	var attrs schema.Labels
	if len(e.GetAttrs()) > 0 {
		attrs = make(schema.Labels, len(e.GetAttrs()))
		for k, v := range e.GetAttrs() {
			attrs[k] = v
		}
	}

	var logRefs []schema.LogRef
	for _, r := range e.GetLogRefs() {
		logRefs = append(logRefs, schema.LogRef{BlockID: r.GetBlockId(), Line: int(r.GetLine())})
	}

	event := schema.Event{
		ID:          e.GetId(),
		TS:          time.UnixMilli(e.GetTsMs()).UTC(),
		HostID:      e.GetHostId(),
		Source:      e.GetSource(),
		Type:        e.GetType(),
		Severity:    schema.Severity(e.GetSeverity()),
		Title:       e.GetTitle(),
		Attrs:       attrs,
		Fingerprint: e.GetFingerprint(),
		LogRefs:     logRefs,
		Schema:      int(e.GetSchema()),
	}
	if subject := e.GetSubject(); subject != nil {
		event.Subject = schema.EventSubject{
			Kind: subject.GetKind(),
			Name: subject.GetName(),
			PID:  int(subject.GetPid()),
		}
	}
	if e.GetTsReceivedMs() != 0 {
		event.TSReceived = time.UnixMilli(e.GetTsReceivedMs()).UTC()
	}
	return event
}

func protoToLogLine(l *bitacorapb.LogLine) schema.LogLine {
	return schema.LogLine{
		TS:              time.UnixMilli(l.GetTsMs()).UTC(),
		HostID:          l.GetHostId(),
		Source:          l.GetSource(),
		Stream:          l.GetStream(),
		UnitOrContainer: l.GetUnitOrContainer(),
		Level:           l.GetLevel(),
		PID:             int(l.GetPid()),
		Message:         l.GetMessage(),
	}
}

func protoToInventory(i *bitacorapb.Inventory) schema.Inventory {
	items := make([]schema.InventoryItem, 0, len(i.GetItems()))
	for _, item := range i.GetItems() {
		attrs := make(schema.Labels, len(item.GetAttrs()))
		for key, value := range item.GetAttrs() {
			attrs[key] = value
		}
		items = append(items, schema.InventoryItem{
			ID:    item.GetId(),
			Name:  item.GetName(),
			Attrs: attrs,
		})
	}
	return schema.Inventory{
		HostID:     i.GetHostId(),
		Kind:       schema.InventoryKind(i.GetKind()),
		ReportedAt: time.UnixMilli(i.GetReportedAtMs()).UTC(),
		Schema:     int(i.GetSchema()),
		Items:      items,
	}
}

func protoToJob(j *bitacorapb.Job) schema.Job {
	job := schema.Job{ID: j.GetId(), JobName: j.GetJobName(), HostID: j.GetHostId(), StartedAt: time.UnixMilli(j.GetStartedAtMs()).UTC(), FinishedAt: time.UnixMilli(j.GetFinishedAtMs()).UTC(), DurationSecond: float64(j.GetDurationMs()) / 1000, Status: schema.JobStatus(j.GetStatus()), ExitCode: int(j.GetExitCode()), Signal: j.GetSignal(), PeerHostID: j.GetPeerHostId(), Trigger: j.GetTrigger(), Schema: int(j.GetSchema())}
	if j.GetNextExpectedMs() != 0 {
		job.NextExpected = time.UnixMilli(j.GetNextExpectedMs()).UTC()
	}
	return job
}
