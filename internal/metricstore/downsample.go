package metricstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Downsample reads every sample for name in [from, to] from src (optionally
// narrowed by extra matchers), averages it into resolution-width buckets
// per series, and appends one aggregated sample per bucket into dst. It
// returns how many bucket-samples were written.
//
// This is a callable, testable operation, not a background job: nothing in
// the codebase yet drives it on a schedule (the agent has no run loop —
// see the followups on #646), so it's exposed for a future scheduler to
// call rather than spawning its own goroutine with nothing to trigger it.
func Downsample(ctx context.Context, src, dst *Store, resolution Resolution, name string, from, to time.Time, extra ...*labels.Matcher) (int, error) {
	width, ok := BucketWidth[resolution]
	if !ok {
		return 0, fmt.Errorf("resolution %q has no bucket width to downsample into", resolution)
	}

	samples, err := src.Query(ctx, name, from, to, extra...)
	if err != nil {
		return 0, fmt.Errorf("querying source samples: %w", err)
	}
	if len(samples) == 0 {
		return 0, nil
	}

	type bucketKey struct {
		seriesKey   string
		bucketStart int64 // unix millis
	}

	sums := map[bucketKey]float64{}
	counts := map[bucketKey]int{}
	labelsByKey := map[string]map[string]string{}

	for _, s := range samples {
		key := seriesLabelKey(s.Labels)
		labelsByKey[key] = s.Labels
		bk := bucketKey{seriesKey: key, bucketStart: s.Timestamp.Truncate(width).UnixMilli()}
		sums[bk] += s.Value
		counts[bk]++
	}

	// tsdb rejects an appended sample whose timestamp is not after the last
	// one committed for the same series. Map iteration order is random, so
	// buckets must be sorted by time (per series) before appending, not
	// written in whatever order ranging over sums happens to produce.
	buckets := make([]bucketKey, 0, len(sums))
	for bk := range sums {
		buckets = append(buckets, bk)
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].seriesKey != buckets[j].seriesKey {
			return buckets[i].seriesKey < buckets[j].seriesKey
		}
		return buckets[i].bucketStart < buckets[j].bucketStart
	})

	written := 0
	for _, bk := range buckets {
		lbls := labelsByKey[bk.seriesKey]
		extraLabels := schema.Labels{}
		for k, v := range lbls {
			if k == "host_id" || k == labels.MetricName {
				continue
			}
			extraLabels[k] = v
		}

		m := schema.Metric{
			Name:      name,
			HostID:    lbls["host_id"],
			Labels:    extraLabels,
			Value:     sums[bk] / float64(counts[bk]),
			Timestamp: time.UnixMilli(bk.bucketStart),
		}
		if err := dst.Append(ctx, m); err != nil {
			return written, fmt.Errorf("appending downsampled bucket: %w", err)
		}
		written++
	}

	return written, nil
}

// seriesLabelKey is a stable string key for a label set, used to group
// samples by series without relying on map iteration order.
func seriesLabelKey(lbls map[string]string) string {
	keys := make([]string, 0, len(lbls))
	for k := range lbls {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(lbls[k])
		b.WriteByte(';')
	}
	return b.String()
}
