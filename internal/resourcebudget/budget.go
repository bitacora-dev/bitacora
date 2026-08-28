// Package resourcebudget checks a process against the agent's resource
// ceiling from ADR-0001: ≤60 MB RSS and ≤2% of one core in steady state.
package resourcebudget

import "fmt"

// Budget from ADR-0001. Do not raise without updating that ADR.
const (
	MaxRSSBytes    uint64  = 60 * 1024 * 1024
	MaxCPUFraction float64 = 0.02
)

// CheckBudget returns an error if rssBytes or cpuFraction exceed the
// ADR-0001 budget.
func CheckBudget(rssBytes uint64, cpuFraction float64) error {
	if rssBytes > MaxRSSBytes {
		return fmt.Errorf("RSS %d bytes exceeds budget of %d bytes", rssBytes, MaxRSSBytes)
	}
	if cpuFraction > MaxCPUFraction {
		return fmt.Errorf("CPU fraction %.4f exceeds budget of %.4f", cpuFraction, MaxCPUFraction)
	}
	return nil
}
