//go:build linux

package resourcebudget

import (
	"os"
	"testing"
)

func TestSample_ReadsCurrentProcess(t *testing.T) {
	rss, cpu, err := Sample(os.Getpid())
	if err != nil {
		t.Fatalf("unexpected error sampling self: %v", err)
	}
	if rss == 0 {
		t.Fatal("expected a nonzero RSS for the running test process")
	}
	if cpu < 0 {
		t.Fatalf("expected non-negative CPU seconds, got %v", cpu)
	}
}

func TestSample_ErrorsOnNonexistentPID(t *testing.T) {
	// PID 1 is always init/systemd and readable; a very large PID is very
	// unlikely to exist.
	if _, _, err := Sample(999999); err == nil {
		t.Fatal("expected an error sampling a nonexistent pid")
	}
}
