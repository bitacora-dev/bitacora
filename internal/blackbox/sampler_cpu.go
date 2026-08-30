package blackbox

import (
	"sort"

	"github.com/prometheus/procfs"
)

// sampleCPU fills NumCPUs/CPUBusyPct from /proc/stat, the same delta-based
// busy ratio as internal/collector/cpu — reimplemented here rather than
// imported because that package's Collector is wired to collector.Sink
// (ADR-0007's runtime), and the black box is deliberately the one
// exception to that path (ADR-0011: "camino de código separado del
// runtime de collectors [...] debe sobrevivir a un agente degradado").
//
// It also sets s.cpuIDs — the sorted list of logical CPU IDs /proc/stat
// currently reports — which every other per-CPU metric (frequency,
// throttling, interrupts) indexes by, so index i always means the same
// logical CPU across every array in the resulting Sample.
func (s *Sampler) sampleCPU(out *Sample) {
	stat, err := s.fs.Stat()
	if err != nil {
		return
	}

	ids := make([]int64, 0, len(stat.CPU))
	for id := range stat.CPU {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > MaxCPUs {
		ids = ids[:MaxCPUs]
	}
	s.cpuIDs = ids
	out.NumCPUs = uint16(len(ids))

	if s.prevCPU != nil && !s.prevAt.IsZero() {
		for i, id := range ids {
			prev, ok := s.prevCPU[id]
			if !ok {
				continue // a CPU that came online since the last sample
			}
			if ratio, ok := cpuBusyRatio(prev, stat.CPU[id]); ok {
				out.CPUBusyPct[i] = float32(ratio * 100)
			}
		}
	}

	s.prevCPU = stat.CPU
}

func cpuBusyRatio(prev, cur procfs.CPUStat) (float64, bool) {
	prevTotal := cpuStatTotal(prev)
	curTotal := cpuStatTotal(cur)
	deltaTotal := curTotal - prevTotal
	if deltaTotal <= 0 {
		return 0, false
	}
	deltaIdle := (cur.Idle + cur.Iowait) - (prev.Idle + prev.Iowait)
	busy := deltaTotal - deltaIdle
	if busy < 0 {
		busy = 0
	}
	return busy / deltaTotal, true
}

func cpuStatTotal(c procfs.CPUStat) float64 {
	return c.User + c.Nice + c.System + c.Idle + c.Iowait + c.IRQ + c.SoftIRQ + c.Steal
}
