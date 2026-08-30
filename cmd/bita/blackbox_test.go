package main

import (
	"strings"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/blackbox"
)

func TestFormatBlackboxSamples_EmptyReportsZero(t *testing.T) {
	out := formatBlackboxSamples(nil)
	if !strings.HasPrefix(out, "0 sample(s)") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestFormatBlackboxSamples_IncludesKeyFields(t *testing.T) {
	samples := []blackbox.Sample{
		{TimestampUnixMilli: 5000, NumCPUs: 4, LoadAvg1: 1.25, MemAvailableKB: 2048, ProcsBlockedD: 2, PSICPUSome10: 3.5},
	}
	out := formatBlackboxSamples(samples)

	if !strings.HasPrefix(out, "1 sample(s)") {
		t.Fatalf("expected a count line, got %q", out)
	}
	for _, want := range []string{"t=5000", "cpus=4", "load1=1.25", "mem_avail_kb=2048", "procs_blocked_d=2", "psi_cpu_some10=3.50"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got %q", want, out)
		}
	}
}

// TestBlackboxDump_ReadsARealRecordedFile is the integration check
// for the CLI's own path: record real samples via the same package
// dump uses, then confirm the formatted output reflects them —
// end-to-end, not just the formatter in isolation.
func TestBlackboxDump_ReadsARealRecordedFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/blackbox.dat"

	rec, err := blackbox.Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.Record(blackbox.Sample{TimestampUnixMilli: 1000, NumCPUs: 2})
	rec.Record(blackbox.Sample{TimestampUnixMilli: 2000, NumCPUs: 2})
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := blackbox.Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := formatBlackboxSamples(samples)
	if !strings.HasPrefix(out, "2 sample(s)") {
		t.Fatalf("expected 2 sample(s), got %q", out)
	}
	if !strings.Contains(out, "t=1000") || !strings.Contains(out, "t=2000") {
		t.Fatalf("expected both timestamps in output, got %q", out)
	}
}
