package blackbox

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// statFixture renders a minimal-but-complete /proc/stat with two CPUs,
// parameterizable by their (idle, active) tick counts so tests can express
// "cpu0 was busy for N ticks" directly.
func statFixture(cpu0Idle, cpu0User, cpu1Idle, cpu1User uint64) string {
	return fmtLine("cpu", cpu0User+cpu1User, cpu0Idle+cpu1Idle) +
		fmtLine("cpu0", cpu0User, cpu0Idle) +
		fmtLine("cpu1", cpu1User, cpu1Idle) +
		"intr 100 1 2\nctxt 500\nbtime 1719400000\nprocesses 100\nprocs_running 3\nprocs_blocked 1\nsoftirq 100 1 2 3 4 5 6 7 8 9 10\n"
}

func fmtLine(label string, user, idle uint64) string {
	// user nice system idle iowait irq softirq steal guest guest_nice
	return label + " " + strconv.FormatUint(user, 10) + " 0 0 " + strconv.FormatUint(idle, 10) + " 0 0 0 0 0 0\n"
}

func newFixtureRoots(t *testing.T) (procRoot, sysRoot string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "bbfx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	procRoot = filepath.Join(dir, "proc")
	sysRoot = filepath.Join(dir, "sys")

	writeFixture(t, filepath.Join(procRoot, "stat"), statFixture(1000, 0, 1000, 0))
	writeFixture(t, filepath.Join(procRoot, "meminfo"), "MemTotal:       16384000 kB\nMemFree: 2048000 kB\nMemAvailable:    8192000 kB\nCached:          3072000 kB\nSwapTotal:       2000000 kB\nSwapFree:        1500000 kB\nDirty:              1200 kB\nWriteback:              0 kB\n")
	writeFixture(t, filepath.Join(procRoot, "loadavg"), "0.50 0.40 0.30 1/200 12345\n")
	writeFixture(t, filepath.Join(procRoot, "interrupts"), "           CPU0       CPU1\n  1:         10          5   IO-APIC   1-edge      timer\n  8:          2          1   IO-APIC   8-edge      rtc0\n")
	writeFixture(t, filepath.Join(procRoot, "pressure", "cpu"), "some avg10=1.50 avg60=0.80 avg300=0.20 total=123456\n")
	writeFixture(t, filepath.Join(procRoot, "pressure", "memory"), "some avg10=0.10 avg60=0.05 avg300=0.01 total=1000\nfull avg10=0.02 avg60=0.01 avg300=0.00 total=200\n")
	writeFixture(t, filepath.Join(procRoot, "pressure", "io"), "some avg10=2.50 avg60=1.10 avg300=0.40 total=9999\nfull avg10=0.50 avg60=0.20 avg300=0.05 total=888\n")
	writeFixture(t, filepath.Join(procRoot, "diskstats"), "8 0 sda 100 0 2000 50 200 0 4000 100 100 300 400 0 0 0 0\n7 0 loop0 5 0 10 1 0 0 0 0 0 0 0 0 0 0\n")

	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_cur_freq"), "3200000\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu0", "thermal_throttle", "core_throttle_count"), "0\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu1", "cpufreq", "scaling_cur_freq"), "3100000\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu1", "thermal_throttle", "core_throttle_count"), "0\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "edac", "mc", "mc0", "ce_count"), "5\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "edac", "mc", "mc0", "ue_count"), "0\n")
	writeFixture(t, filepath.Join(sysRoot, "class", "hwmon", "hwmon0", "temp1_input"), "45000\n")
	writeFixture(t, filepath.Join(sysRoot, "class", "hwmon", "hwmon1", "temp1_input"), "62000\n")

	return procRoot, sysRoot
}

func TestSampler_FirstSample_NoDeltaFieldsYetButStaticFieldsPresent(t *testing.T) {
	procRoot, sysRoot := newFixtureRoots(t)
	s, err := NewSampler(procRoot, sysRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sample := s.Sample(time.Unix(1000, 0))

	if sample.NumCPUs != 2 {
		t.Fatalf("expected 2 CPUs, got %d", sample.NumCPUs)
	}
	if sample.CPUFreqMHz[0] != 3200 || sample.CPUFreqMHz[1] != 3100 {
		t.Fatalf("unexpected CPUFreqMHz: %v", sample.CPUFreqMHz[:2])
	}
	if sample.MemTotalKB != 16384000 || sample.MemAvailableKB != 8192000 {
		t.Fatalf("unexpected memory fields: total=%d available=%d", sample.MemTotalKB, sample.MemAvailableKB)
	}
	if sample.MemSwapUsedKB != 500000 {
		t.Fatalf("expected swap used 500000 (2000000-1500000), got %d", sample.MemSwapUsedKB)
	}
	if sample.LoadAvg1 != 0.5 {
		t.Fatalf("expected LoadAvg1 0.5, got %v", sample.LoadAvg1)
	}
	if sample.ProcsRunnable != 3 || sample.ProcsBlockedD != 1 {
		t.Fatalf("unexpected proc counts: runnable=%d blocked=%d", sample.ProcsRunnable, sample.ProcsBlockedD)
	}
	if sample.NumSensors != 2 || sample.SensorTempMilliC[0] != 45000 || sample.SensorTempMilliC[1] != 62000 {
		t.Fatalf("unexpected sensors: n=%d values=%v", sample.NumSensors, sample.SensorTempMilliC[:2])
	}
	if sample.PSICPUSome10 != 1.5 || sample.PSIMemSome10 != 0.1 || sample.PSIMemFull10 != 0.02 || sample.PSIIOSome10 != 2.5 || sample.PSIIOFull10 != 0.5 {
		t.Fatalf("unexpected PSI values: %+v", sample)
	}
	if sample.NumBlockDevices != 1 || sample.BlockQueueDepth[0] != 100 {
		t.Fatalf("expected 1 real block device (sda, loop0 filtered) with queue depth 100, got n=%d q=%v", sample.NumBlockDevices, sample.BlockQueueDepth[:1])
	}
	// Deltas (interrupts, EDAC, block latency, throttle) aren't available
	// on the very first sample — nothing to diff against yet.
	if sample.InterruptsPerCPU[0] != 0 || sample.EDACCorrectableDelta != 0 || sample.BlockLatencyUs[0] != 0 {
		t.Fatalf("expected zero deltas on the first sample, got %+v", sample)
	}
}

func TestSampler_SecondSample_ComputesDeltasAndBusyPercent(t *testing.T) {
	procRoot, sysRoot := newFixtureRoots(t)
	s, err := NewSampler(procRoot, sysRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.Sample(time.Unix(1000, 0))

	// cpu0 goes fully busy (idle unchanged, user +1000); cpu1 stays idle.
	writeFixture(t, filepath.Join(procRoot, "stat"), statFixture(1000, 1000, 2000, 0))
	writeFixture(t, filepath.Join(procRoot, "interrupts"), "           CPU0       CPU1\n  1:         25         10   IO-APIC   1-edge      timer\n  8:          4          1   IO-APIC   8-edge      rtc0\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "cpu", "cpu0", "thermal_throttle", "core_throttle_count"), "3\n")
	writeFixture(t, filepath.Join(sysRoot, "devices", "system", "edac", "mc", "mc0", "ce_count"), "8\n")
	writeFixture(t, filepath.Join(procRoot, "diskstats"), "8 0 sda 100 0 2000 50 260 0 5200 220 0 900 1200 0 0 0 0\n7 0 loop0 5 0 10 1 0 0 0 0 0 0 0 0 0 0\n")

	sample := s.Sample(time.Unix(1001, 0))

	if sample.CPUBusyPct[0] <= 90 {
		t.Fatalf("expected cpu0 to read as nearly 100%% busy, got %v", sample.CPUBusyPct[0])
	}
	if sample.CPUBusyPct[1] != 0 {
		t.Fatalf("expected cpu1 to read as idle, got %v", sample.CPUBusyPct[1])
	}
	if sample.ThrottledMask&1 == 0 {
		t.Fatalf("expected cpu0's throttle bit set (count 0 -> 3), got mask %b", sample.ThrottledMask)
	}
	if sample.ThrottledMask&2 != 0 {
		t.Fatalf("expected cpu1's throttle bit clear, got mask %b", sample.ThrottledMask)
	}
	// interrupts: CPU0 25-10=15, CPU1 10-5... wait deltas sum both IRQ lines.
	if sample.InterruptsPerCPU[0] != (25-10)+(4-2) {
		t.Fatalf("unexpected interrupt delta for cpu0: %d", sample.InterruptsPerCPU[0])
	}
	if sample.EDACCorrectableDelta != 3 {
		t.Fatalf("expected EDAC correctable delta 3 (8-5), got %d", sample.EDACCorrectableDelta)
	}
	if sample.BlockLatencyUs[0] == 0 {
		t.Fatalf("expected a non-zero block latency estimate on the second sample")
	}
}

func TestSampler_MissingOptionalPathsDegradeGracefully(t *testing.T) {
	dir, err := os.MkdirTemp("", "bbfx2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	procRoot := filepath.Join(dir, "proc")
	sysRoot := filepath.Join(dir, "sys") // deliberately empty: no hwmon, no edac, no cpufreq

	writeFixture(t, filepath.Join(procRoot, "stat"), statFixture(1000, 0, 1000, 0))
	writeFixture(t, filepath.Join(procRoot, "meminfo"), "MemTotal: 1000 kB\n")
	writeFixture(t, filepath.Join(procRoot, "loadavg"), "0.1 0.1 0.1 1/1 1\n")

	s, err := NewSampler(procRoot, sysRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sample := s.Sample(time.Unix(1000, 0)) // must not panic or error despite everything else missing

	if sample.NumSensors != 0 || sample.NumBlockDevices != 0 || sample.EDACCorrectableDelta != 0 {
		t.Fatalf("expected zero values for unavailable metric groups, got %+v", sample)
	}
	if sample.NumCPUs != 2 {
		t.Fatalf("expected /proc/stat's 2 CPUs to still be read, got %d", sample.NumCPUs)
	}
}
