package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func TestDownsample_AveragesRawSamplesIntoBuckets(t *testing.T) {
	raw := newTestStore(t)
	oneMin := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	// Three raw samples inside the same 1-minute bucket: 0.10, 0.20, 0.30.
	for i, v := range []float64{0.10, 0.20, 0.30} {
		m := schema.Metric{
			Name:      "bitacora_cpu_usage_ratio",
			HostID:    "host-a",
			Labels:    schema.Labels{"cpu": "0"},
			Value:     v,
			Timestamp: base.Add(time.Duration(i*10) * time.Second),
		}
		if err := raw.Append(ctx, m); err != nil {
			t.Fatalf("unexpected error appending raw sample: %v", err)
		}
	}

	n, err := Downsample(ctx, raw, oneMin, Resolution1m, "bitacora_cpu_usage_ratio", base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error downsampling: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 bucket written, got %d", n)
	}

	got, err := oneMin.Query(ctx, "bitacora_cpu_usage_ratio", base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error querying downsampled store: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 downsampled sample, got %d", len(got))
	}

	wantAvg := (0.10 + 0.20 + 0.30) / 3
	if diff := got[0].Value - wantAvg; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected averaged value %.6f, got %.6f", wantAvg, got[0].Value)
	}
	if got[0].Labels["host_id"] != "host-a" || got[0].Labels["cpu"] != "0" {
		t.Fatalf("expected downsampled sample to keep original labels, got %+v", got[0].Labels)
	}
}

func TestDownsample_SeparatesDistinctBuckets(t *testing.T) {
	raw := newTestStore(t)
	oneMin := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	samples := []struct {
		offset time.Duration
		value  float64
	}{
		{0, 0.10},
		{30 * time.Second, 0.20}, // same minute as above
		{90 * time.Second, 0.90}, // next minute
	}
	for _, s := range samples {
		m := schema.Metric{Name: "bitacora_cpu_usage_ratio", HostID: "host-a", Value: s.value, Timestamp: base.Add(s.offset)}
		if err := raw.Append(ctx, m); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	n, err := Downsample(ctx, raw, oneMin, Resolution1m, "bitacora_cpu_usage_ratio", base.Add(-time.Minute), base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 distinct buckets, got %d", n)
	}
}

func TestDownsample_NoSamplesIsANoOp(t *testing.T) {
	raw := newTestStore(t)
	oneMin := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)

	n, err := Downsample(ctx, raw, oneMin, Resolution1m, "bitacora_cpu_usage_ratio", base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 buckets written when there's nothing to downsample, got %d", n)
	}
}

func TestDownsample_RejectsResolutionRaw(t *testing.T) {
	raw := newTestStore(t)
	other := newTestStore(t)
	base := time.Now()

	if _, err := Downsample(context.Background(), raw, other, ResolutionRaw, "bitacora_cpu_usage_ratio", base.Add(-time.Minute), base); err == nil {
		t.Fatal("expected an error downsampling into ResolutionRaw, which has no bucket width")
	}
}
