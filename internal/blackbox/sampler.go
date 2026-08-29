package blackbox

import (
	"time"

	"github.com/prometheus/procfs"
)

// Sampler gathers one Sample from /proc and /sys. It holds the previous
// raw readings for every metric that's a cumulative counter upstream
// (interrupts, EDAC, block I/O ticks) so it can report the delta ADR-0011
// actually wants ("deltas de /proc/interrupts") instead of an
// ever-growing total. ProcRoot/SysRoot are injectable — tests point them
// at testdata instead of the real /proc, /sys, the same pattern every
// other collector in this repo uses.
type Sampler struct {
	ProcRoot string
	SysRoot  string

	fs procfs.FS

	prevAt time.Time
	cpuIDs []int64 // sorted logical CPU IDs from the current sample's /proc/stat

	prevCPU       map[int64]procfs.CPUStat
	prevThrottle  map[int64]uint64 // logical CPU -> cumulative core_throttle_count
	prevInterrupt map[int64]uint64 // logical CPU -> cumulative interrupt count
	prevBlock     map[string]blockCounters
	prevEDACCe    uint64
	prevEDACUe    uint64
}

// NewSampler opens procfs at procRoot. sysRoot is used directly by the
// sysfs-only readers (hwmon, cpufreq, topology, EDAC, PSI, throttling)
// that this package hand-parses instead of going through procfs's own
// sysfs helpers — kept simple and dependency-free rather than committing
// to procfs's evolving sysfs API surface for each of them.
func NewSampler(procRoot, sysRoot string) (*Sampler, error) {
	fs, err := procfs.NewFS(procRoot)
	if err != nil {
		return nil, err
	}
	return &Sampler{ProcRoot: procRoot, SysRoot: sysRoot, fs: fs}, nil
}

// Sample gathers everything ADR-0011 lists, best-effort: a metric group
// this host doesn't support (no hwmon, no EDAC, ...) is simply left at
// its zero value rather than failing the whole sample — a partial sample
// is far more useful than none, and "what's actually available" varies a
// lot across the hosts this project targets (ADR-0004).
func (s *Sampler) Sample(now time.Time) Sample {
	var out Sample
	out.TimestampUnixMilli = now.UnixMilli()

	s.sampleCPU(&out)
	s.sampleCPUFreqAndThrottle(&out)
	s.sampleSensors(&out)
	s.sampleMemory(&out)
	s.sampleLoadAndProcs(&out)
	s.sampleInterrupts(&out)
	s.samplePSI(&out)
	s.sampleBlockDevices(&out)
	s.sampleEDAC(&out)

	s.prevAt = now
	return out
}
