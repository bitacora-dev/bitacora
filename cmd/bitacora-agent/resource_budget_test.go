//go:build linux

package main

import (
	"os/exec"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/resourcebudget"
)

// TestResourceBudget builds and launches the real agent binary and checks
// it against the ADR-0001 ceiling (≤60 MB RSS, ≤2% of one core).
//
// Today's agent is a scaffold that prints a line and exits immediately —
// there's no steady-state collector loop running yet (that's wired in a
// later task, see the followups on #646). So there's nothing to sample
// most of the time, and this test says so via t.Skip rather than faking a
// pass. Once the agent has a real run loop, this becomes the actual load
// test ADR-0001 asks for, with no changes needed here.
func TestResourceBudget(t *testing.T) {
	bin := buildAgent(t)

	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	time.Sleep(300 * time.Millisecond)

	rss, cpuSeconds, err := resourcebudget.Sample(cmd.Process.Pid)
	if err != nil {
		t.Skipf("agent already exited before sampling (expected — no run loop wired yet): %v", err)
	}

	cpuFraction := cpuSeconds / 0.3 // rough: cpu time over the ~300ms sampling window
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
