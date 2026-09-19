package agentbuffer

import (
	"context"
	"math/rand"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	DefaultOutboundDir   = "/var/lib/bitacora/spool/outbound"
	DefaultFlushInterval = 15 * time.Second
	DefaultFlushBackoff  = time.Second
	MaxFlushBackoff      = time.Minute
)

type Logger func(format string, args ...any)

type Sink struct {
	HostID string
	Buffer *Buffer
	Now    func() time.Time
	Logf   Logger

	flushCh chan struct{}
}

func NewSink(hostID string, buffer *Buffer, opts ...SinkOption) *Sink {
	s := &Sink{
		HostID:  hostID,
		Buffer:  buffer,
		Now:     time.Now,
		Logf:    func(string, ...any) {},
		flushCh: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type SinkOption func(*Sink)

func WithClock(now func() time.Time) SinkOption {
	return func(s *Sink) {
		if now != nil {
			s.Now = now
		}
	}
}

func WithLogger(logf Logger) SinkOption {
	return func(s *Sink) {
		if logf != nil {
			s.Logf = logf
		}
	}
}

// BeginCycle implements collector.CycleSink: it returns a Sink that stamps
// every emission with `now` instead of reading the clock again per
// emission. The returned value is a shallow copy with a frozen clock — it
// shares the same Buffer and flush channel, so buffering and flushing are
// unchanged, and the receiver itself is untouched and still usable
// concurrently by other collectors.
//
// Collectors that set a timestamp explicitly (journald's log lines carry
// the journal's own instant, an Inventory its ReportedAt) keep it: the
// frozen clock only fills in what would otherwise have been time.Now().
func (s *Sink) BeginCycle(now time.Time) collector.Sink {
	if s == nil {
		return s
	}
	frozen := *s
	frozen.Now = func() time.Time { return now }
	return &frozen
}

func (s *Sink) Gauge(name string, value float64, labels collector.Labels) {
	now := s.Now()
	s.append(Item{
		Priority: PriorityMetric,
		TS:       now,
		Metric: &schema.Metric{
			Name:      name,
			HostID:    s.HostID,
			Labels:    cloneLabels(labels),
			Value:     value,
			Timestamp: now,
		},
	})
}

func (s *Sink) Counter(name string, value float64, labels collector.Labels) {
	s.Gauge(name, value, labels)
}

func (s *Sink) Event(e collector.Event) {
	if e.HostID == "" {
		e.HostID = s.HostID
	}
	if e.TS.IsZero() {
		e.TS = s.Now()
	}
	if e.Schema == 0 {
		e.Schema = schema.CurrentSchemaVersion
	}
	s.append(Item{Priority: PriorityEvent, TS: e.TS, Event: &e})
}

func (s *Sink) LogLines(source string, lines []collector.LogLine) {
	for _, line := range lines {
		if line.HostID == "" {
			line.HostID = s.HostID
		}
		if line.Source == "" {
			line.Source = source
		}
		if line.TS.IsZero() {
			line.TS = s.Now()
		}
		s.append(Item{Priority: PriorityLogLine, TS: line.TS, LogLine: &line})
	}
}

func (s *Sink) Inventory(inv collector.Inventory) {
	if inv.HostID == "" {
		inv.HostID = s.HostID
	}
	if inv.ReportedAt.IsZero() {
		inv.ReportedAt = s.Now()
	}
	if inv.Schema == 0 {
		inv.Schema = schema.CurrentSchemaVersion
	}
	s.append(Item{Priority: PriorityEvent, TS: inv.ReportedAt, Inventory: &inv})
}

// Job queues a completed operation with the same durable, non-discardable
// priority as events. Stats deliberately remain nil when the source has no
// trustworthy values rather than being populated with zeroes.
func (s *Sink) Job(job collector.Job) {
	if job.HostID == "" {
		job.HostID = s.HostID
	}
	if job.Schema == 0 {
		job.Schema = schema.CurrentSchemaVersion
	}
	s.append(Item{Priority: PriorityEvent, TS: job.FinishedAt, Job: &job})
}

func (s *Sink) append(item Item) {
	if s == nil || s.Buffer == nil {
		return
	}
	if _, err := s.Buffer.Append(item); err != nil {
		s.Logf("bitacora-agent: buffering telemetry: %v", err)
		return
	}
	s.TriggerFlush()
}

func (s *Sink) TriggerFlush() {
	select {
	case s.flushCh <- struct{}{}:
	default:
	}
}

type FlushOptions struct {
	Interval time.Duration
	// PollInterval returns the delay to the next ingest poll. Nil preserves the
	// configured interval. The action channel uses it only after locally
	// accepting a pending order.
	PollInterval func(time.Duration) time.Duration
	// Poll runs when there is no buffered telemetry. Nil preserves the old
	// behaviour of doing no network work for an empty buffer.
	Poll       func(context.Context) error
	BatchSize  int
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

func (s *Sink) Run(ctx context.Context, sender Sender, opts FlushOptions) {
	if sender == nil {
		return
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	minBackoff := opts.MinBackoff
	if minBackoff <= 0 {
		minBackoff = DefaultFlushBackoff
	}
	maxBackoff := opts.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = MaxFlushBackoff
	}

	backoff := minBackoff
	for {
		pollInterval := interval
		if opts.PollInterval != nil {
			pollInterval = opts.PollInterval(interval)
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.flushCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		if s.Buffer == nil || s.Buffer.Len() == 0 {
			if opts.Poll != nil {
				if err := opts.Poll(ctx); err != nil {
					s.Logf("bitacora-agent: polling ingest response failed: %v", err)
				}
			}
			backoff = minBackoff
			continue
		}

		if err := s.Buffer.Backfill(ctx, sender, BackfillOptions{BatchSize: opts.BatchSize}); err != nil {
			s.Logf("bitacora-agent: sending telemetry failed; will retry: %v", err)
			wait := jitter(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			s.TriggerFlush()
			continue
		}
		backoff = minBackoff
	}
}

func cloneLabels(labels schema.Labels) schema.Labels {
	if labels == nil {
		return nil
	}
	out := make(schema.Labels, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	spread := d / 2
	return spread + time.Duration(rand.Int63n(int64(spread)+1))
}

var (
	_ collector.Sink      = (*Sink)(nil)
	_ collector.CycleSink = (*Sink)(nil)
)
