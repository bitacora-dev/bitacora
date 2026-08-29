package blackbox

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGarbage(path string) error {
	garbage := make([]byte, headerSize*2)
	for i := range garbage {
		garbage[i] = 'x'
	}
	return os.WriteFile(path, garbage, 0o644)
}

func sampleAt(ms int64) Sample {
	s := Sample{TimestampUnixMilli: ms, NumCPUs: 2}
	s.CPUBusyPct[0] = 10
	s.CPUBusyPct[1] = 20
	return s
}

func TestRecorder_RecordThenDump_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	rec, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.Record(sampleAt(1000))
	rec.Record(sampleAt(2000))
	rec.Record(sampleAt(3000))
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}
	for i, want := range []int64{1000, 2000, 3000} {
		if samples[i].TimestampUnixMilli != want {
			t.Errorf("sample %d: TimestampUnixMilli = %d, want %d", i, samples[i].TimestampUnixMilli, want)
		}
	}
	if samples[0].CPUBusyPct[0] != 10 || samples[0].CPUBusyPct[1] != 20 {
		t.Errorf("unexpected CPUBusyPct: %v", samples[0].CPUBusyPct)
	}
}

func TestRecorder_WrapsAroundCapacity_OldestFirstOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	rec, err := Open(path, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Capacity 3, write 5 — slots 0 and 1 get overwritten by the 4th/5th.
	for i := int64(1); i <= 5; i++ {
		rec.Record(sampleAt(i * 1000))
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples (capacity), got %d", len(samples))
	}
	want := []int64{3000, 4000, 5000}
	for i, w := range want {
		if samples[i].TimestampUnixMilli != w {
			t.Errorf("sample %d: TimestampUnixMilli = %d, want %d", i, samples[i].TimestampUnixMilli, w)
		}
	}
}

func TestRecorder_PartialRing_DumpsOnlyWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	rec, err := Open(path, 900)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.Record(sampleAt(1000))
	rec.Record(sampleAt(2000))
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples out of 900 capacity, got %d", len(samples))
	}
}

func TestRecorder_ResumesWriteIndexAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	rec, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.Record(sampleAt(1000))
	rec.Record(sampleAt(2000))
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec2, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec2.Record(sampleAt(3000))
	if err := rec2.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("expected recording to resume (3 total samples), got %d", len(samples))
	}
}

func TestRecorder_CapacityMismatchReinitializesRatherThanMisreading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	rec, err := Open(path, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.Record(sampleAt(1000))
	if err := rec.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec2, err := Open(path, 20) // different capacity
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := rec2.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("expected a fresh, empty ring after a capacity change, got %d samples", len(samples))
	}
}

func TestDump_RejectsAFileWithoutTheRightMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-blackbox.dat")
	if err := writeGarbage(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dump(path); err == nil {
		t.Fatal("expected an error dumping a file that isn't a blackbox file")
	}
}
