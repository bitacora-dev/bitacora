// Package metricstore embeds prometheus/tsdb as Capa 1 of ADR-0003: metric
// storage, with three separately-retained resolutions (raw/1m/5m) rather
// than a single database with one retention policy.
package metricstore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Resolution is one of the three downsampling tiers ADR-0003 defines.
type Resolution string

// The three resolutions ADR-0003 mandates, with their bucket width and
// default retention.
const (
	ResolutionRaw Resolution = "raw" // 10s samples, 7 days
	Resolution1m  Resolution = "1m"  // 1 minute buckets, 90 days
	Resolution5m  Resolution = "5m"  // 5 minute buckets, 2 years
)

// BucketWidth is the downsampling window for a resolution. ResolutionRaw
// has none — it stores samples as collected, not bucketed.
var BucketWidth = map[Resolution]time.Duration{
	Resolution1m: time.Minute,
	Resolution5m: 5 * time.Minute,
}

// DefaultRetention is ADR-0003's default retention per resolution.
var DefaultRetention = map[Resolution]time.Duration{
	ResolutionRaw: 7 * 24 * time.Hour,
	Resolution1m:  90 * 24 * time.Hour,
	Resolution5m:  2 * 365 * 24 * time.Hour,
}

// Store wraps one embedded tsdb.DB — one resolution's worth of data. A
// deployment opens three (see DefaultRetention), one directory each.
type Store struct {
	db *tsdb.DB
}

// Open opens (creating if needed) a tsdb database at dir with the given
// retention.
func Open(dir string, retention time.Duration) (*Store, error) {
	opts := tsdb.DefaultOptions()
	opts.RetentionDuration = retention.Milliseconds()

	// tsdb logs are noisy at info level and there's no logging pipeline
	// wired up yet (that's the agent/hub's job, not this package's);
	// discard rather than spam stderr.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := tsdb.Open(dir, logger, nil, opts, nil)
	if err != nil {
		return nil, fmt.Errorf("opening tsdb at %s: %w", dir, err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying tsdb database.
func (s *Store) Close() error { return s.db.Close() }

// Append writes one metric sample.
func (s *Store) Append(ctx context.Context, m schema.Metric) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("invalid metric: %w", err)
	}

	app := s.db.Appender(ctx)
	if _, err := app.Append(0, toLabels(m), m.Timestamp.UnixMilli(), m.Value); err != nil {
		_ = app.Rollback()
		return fmt.Errorf("appending %s: %w", m.Name, err)
	}
	return app.Commit()
}

// Sample is one point of a queried series, with its full label set —
// including __name__ and host_id, so a caller doesn't need the original
// query's matchers to know what it got back.
type Sample struct {
	Labels    map[string]string
	Timestamp time.Time
	Value     float64
}

// Query returns every sample for metric name in [from, to], optionally
// narrowed by extra label matchers (e.g. host_id).
func (s *Store) Query(ctx context.Context, name string, from, to time.Time, extra ...*labels.Matcher) ([]Sample, error) {
	q, err := s.db.Querier(from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}
	defer q.Close()

	matchers := append([]*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, name)}, extra...)
	ss := q.Select(ctx, false, nil, matchers...)

	var samples []Sample
	var it chunkenc.Iterator
	for ss.Next() {
		series := ss.At()
		lbls := series.Labels().Map()
		it = series.Iterator(it)
		for it.Next() == chunkenc.ValFloat {
			t, v := it.At()
			samples = append(samples, Sample{Labels: lbls, Timestamp: time.UnixMilli(t), Value: v})
		}
		if err := it.Err(); err != nil {
			return nil, fmt.Errorf("iterating series: %w", err)
		}
	}
	if err := ss.Err(); err != nil {
		return nil, fmt.Errorf("selecting series: %w", err)
	}

	return samples, nil
}

func toLabels(m schema.Metric) labels.Labels {
	pairs := make([]string, 0, 4+2*len(m.Labels))
	pairs = append(pairs, labels.MetricName, m.Name, "host_id", m.HostID)
	for k, v := range m.Labels {
		pairs = append(pairs, k, v)
	}
	return labels.FromStrings(pairs...)
}
