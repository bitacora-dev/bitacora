package blackbox

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sampleMemory fills the memory fields from /proc/meminfo, via procfs
// (already a project dependency — internal/collector/memory reads it the
// same way).
func (s *Sampler) sampleMemory(out *Sample) {
	mi, err := s.fs.Meminfo()
	if err != nil {
		return
	}
	if mi.MemTotal != nil {
		out.MemTotalKB = *mi.MemTotal
	}
	if mi.MemAvailable != nil {
		out.MemAvailableKB = *mi.MemAvailable
	}
	if mi.Cached != nil {
		out.MemCachedKB = *mi.Cached
	}
	if mi.Dirty != nil {
		out.MemDirtyKB = *mi.Dirty
	}
	if mi.Writeback != nil {
		out.MemWritebackKB = *mi.Writeback
	}
	if mi.SwapTotal != nil && mi.SwapFree != nil && *mi.SwapTotal >= *mi.SwapFree {
		out.MemSwapUsedKB = *mi.SwapTotal - *mi.SwapFree
	}
}

// sampleLoadAndProcs fills LoadAvg1 and the runnable/D-state process
// counts, from /proc/loadavg and /proc/stat respectively.
func (s *Sampler) sampleLoadAndProcs(out *Sample) {
	if la, err := s.fs.LoadAvg(); err == nil {
		out.LoadAvg1 = float32(la.Load1)
	}
	if stat, err := s.fs.Stat(); err == nil {
		out.ProcsRunnable = uint32(stat.ProcessesRunning)
		out.ProcsBlockedD = uint32(stat.ProcessesBlocked)
	}
}

// sampleInterrupts parses /proc/interrupts and sums, per logical CPU, the
// interrupt count across every IRQ line — ADR-0011 wants "interrupciones
// por CPU", not a full per-line breakdown, which would blow well past the
// "leer barato" budget. Reports the delta since the previous sample,
// indexed to match s.cpuIDs.
func (s *Sampler) sampleInterrupts(out *Sample) {
	f, err := os.Open(filepath.Join(s.ProcRoot, "interrupts"))
	if err != nil {
		return
	}
	defer f.Close()

	totals, ok := parseInterruptTotals(f, len(s.cpuIDs))
	if !ok {
		return
	}

	cur := make(map[int64]uint64, len(s.cpuIDs))
	for i, id := range s.cpuIDs {
		if i >= len(totals) {
			break
		}
		cur[id] = totals[i]
		if prev, ok := s.prevInterrupt[id]; ok && !s.prevAt.IsZero() {
			out.InterruptsPerCPU[i] = deltaUint64(prev, totals[i])
		}
	}
	s.prevInterrupt = cur
}

// parseInterruptTotals sums every numeric per-CPU column across all IRQ
// lines in /proc/interrupts' format: a header line of "CPU0 CPU1 ...",
// then one line per IRQ of "label: count0 count1 ... description".
// Non-numeric IRQ lines (a description-only trailer) and non-numeric
// trailing columns are simply not counted — /proc/interrupts has no fixed
// column count across kernel versions.
func parseInterruptTotals(f *os.File, numCPUs int) ([]uint64, bool) {
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return nil, false
	}
	// Header line establishes how many CPU columns exist; cap at numCPUs
	// so a mismatch with s.cpuIDs (a CPU came online between reads)
	// degrades gracefully instead of panicking on an out-of-range index.
	header := strings.Fields(scanner.Text())
	n := len(header)
	if numCPUs > 0 && numCPUs < n {
		n = numCPUs
	}
	totals := make([]uint64, n)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		for i := 0; i < n && i+1 < len(fields); i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				continue // reached the description column, or a non-numeric IRQ line
			}
			totals[i] += v
		}
	}
	return totals, true
}

// samplePSI parses /proc/pressure/{cpu,memory,io}'s "some"/"full" lines'
// avg10 field — ADR-0011: "el indicador más temprano de degradación
// disponible en Linux." Older kernels expose only "some" for cpu; a
// missing line or file is left at zero rather than treated as an error,
// since PSI support itself is a kernel config option.
func (s *Sampler) samplePSI(out *Sample) {
	if v, ok := readPSIAvg10(filepath.Join(s.ProcRoot, "pressure", "cpu"), "some"); ok {
		out.PSICPUSome10 = v
	}
	if v, ok := readPSIAvg10(filepath.Join(s.ProcRoot, "pressure", "memory"), "some"); ok {
		out.PSIMemSome10 = v
	}
	if v, ok := readPSIAvg10(filepath.Join(s.ProcRoot, "pressure", "memory"), "full"); ok {
		out.PSIMemFull10 = v
	}
	if v, ok := readPSIAvg10(filepath.Join(s.ProcRoot, "pressure", "io"), "some"); ok {
		out.PSIIOSome10 = v
	}
	if v, ok := readPSIAvg10(filepath.Join(s.ProcRoot, "pressure", "io"), "full"); ok {
		out.PSIIOFull10 = v
	}
}

func readPSIAvg10(path, line string) (float32, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(l)
		if len(fields) == 0 || fields[0] != line {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				n, err := strconv.ParseFloat(v, 32)
				if err != nil {
					return 0, false
				}
				return float32(n), true
			}
		}
	}
	return 0, false
}
