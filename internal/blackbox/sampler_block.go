package blackbox

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// blockCounters is one device's cumulative counters from /proc/diskstats,
// kept from the previous sample to compute an average I/O latency delta.
type blockCounters struct {
	ios             uint64 // reads + writes completed
	weightedIOTicks uint64 // ms, cumulative time spent with I/O outstanding, weighted by queue depth
}

// sampleBlockDevices reads /proc/diskstats. Queue depth (field 12,
// "I/Os currently in progress") is an instantaneous value, read directly;
// average I/O latency has no direct field and is estimated the same way
// iostat does — delta(weighted_io_ticks) / delta(ios) — which needs the
// previous sample, so it's zero on the first one. Devices are limited to
// real disks: loop/dm/md/ram/zram are noise for this purpose, same
// filtering internal/collector/docker and bitacora-smart already apply to
// their own device enumeration.
func (s *Sampler) sampleBlockDevices(out *Sample) {
	f, err := os.Open(filepath.Join(s.ProcRoot, "diskstats"))
	if err != nil {
		return
	}
	defer f.Close()

	type row struct {
		name            string
		inProgress      uint32
		ios             uint64
		weightedIOTicks uint64
	}
	var rows []row

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		if isVirtualBlockDevice(name) {
			continue
		}
		rdIOs, err1 := strconv.ParseUint(fields[3], 10, 64)
		wrIOs, err2 := strconv.ParseUint(fields[7], 10, 64)
		inProgress, err3 := strconv.ParseUint(fields[11], 10, 32)
		weightedTicks, err4 := strconv.ParseUint(fields[13], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		rows = append(rows, row{
			name:            name,
			inProgress:      uint32(inProgress),
			ios:             rdIOs + wrIOs,
			weightedIOTicks: weightedTicks,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	cur := make(map[string]blockCounters, len(rows))
	n := 0
	for _, r := range rows {
		if n >= MaxBlockDevices {
			break
		}
		out.BlockQueueDepth[n] = r.inProgress

		cur[r.name] = blockCounters{ios: r.ios, weightedIOTicks: r.weightedIOTicks}
		if prev, ok := s.prevBlock[r.name]; ok && !s.prevAt.IsZero() {
			deltaIOs := deltaUint64(prev.ios, r.ios)
			deltaTicks := deltaUint64(prev.weightedIOTicks, r.weightedIOTicks)
			if deltaIOs > 0 {
				out.BlockLatencyUs[n] = uint32(deltaTicks * 1000 / deltaIOs)
			}
		}
		n++
	}
	out.NumBlockDevices = uint16(n)
	s.prevBlock = cur
}

var virtualBlockPrefixes = []string{"loop", "ram", "sr", "dm-", "md", "zram"}

func isVirtualBlockDevice(name string) bool {
	for _, p := range virtualBlockPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
