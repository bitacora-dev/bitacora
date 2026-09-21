//go:build linux

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/resourcebudget"
)

// TestResourceBudget builds and launches the real agent binary and checks
// it against the ADR-0001 ceiling (≤60 MB RSS, ≤2% of one core).
func TestResourceBudget(t *testing.T) {
	bin := buildAgent(t)

	state := t.TempDir()
	cmd := exec.Command(bin,
		"-host-id-path", filepath.Join(state, "host_id"),
		"-spool-dir", filepath.Join(state, "spool"),
		"-blackbox-path", filepath.Join(state, "blackbox.dat"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait through at least one ADR-0011 sample so the measured process includes
	// the mmap-backed recorder's actual steady-state work, not only its startup.
	time.Sleep(1500 * time.Millisecond)

	_, beforeCPU, err := resourcebudget.Sample(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("agent exited before its baseline resource sample: %v", err)
	}
	started := time.Now()
	const measurementWindow = 10 * time.Second
	time.Sleep(measurementWindow)
	rss, afterCPU, err := resourcebudget.Sample(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("agent exited before its steady-state resource sample: %v", err)
	}
	cpuFraction := (afterCPU - beforeCPU) / time.Since(started).Seconds()
	t.Logf("agent with blackbox: rss_bytes=%d cpu_fraction=%.6f window=%s", rss, cpuFraction, time.Since(started).Round(time.Millisecond))
	if err := resourcebudget.CheckBudget(rss, cpuFraction); err != nil {
		t.Fatalf("agent exceeded ADR-0001 resource budget: %v", err)
	}
}

func buildAgent(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/bitacora-agent"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building agent: %v\n%s", err, out)
	}
	return bin
}
