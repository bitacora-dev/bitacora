//go:build !linux

package resourcebudget

import "errors"

// ErrUnsupported reports that this platform does not expose Linux procfs
// counters, so resource budget measurements cannot be collected honestly.
var ErrUnsupported = errors.New("resource budget monitoring is only available on Linux")

// Sample is unavailable outside Linux because the resource budget evidence is
// defined in terms of /proc/<pid>/status and /proc/<pid>/stat.
func Sample(int) (uint64, float64, error) {
	return 0, 0, ErrUnsupported
}
