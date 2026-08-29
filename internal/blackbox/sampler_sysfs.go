package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sampleCPUFreqAndThrottle reads each logical CPU's current scaling
// frequency and thermal-throttle event count from sysfs, indexed to match
// s.cpuIDs (set by sampleCPU) so CPUFreqMHz[i] and ThrottledMask bit i
// describe the same logical CPU as CPUBusyPct[i].
func (s *Sampler) sampleCPUFreqAndThrottle(out *Sample) {
	if len(s.cpuIDs) == 0 {
		return
	}

	curThrottle := make(map[int64]uint64, len(s.cpuIDs))

	for i, id := range s.cpuIDs {
		base := filepath.Join(s.SysRoot, "devices", "system", "cpu", fmt.Sprintf("cpu%d", id))

		if khz, err := readUintFile(filepath.Join(base, "cpufreq", "scaling_cur_freq")); err == nil {
			out.CPUFreqMHz[i] = uint32(khz / 1000)
		}

		if count, err := readUintFile(filepath.Join(base, "thermal_throttle", "core_throttle_count")); err == nil {
			curThrottle[id] = count
			if prev, ok := s.prevThrottle[id]; ok && count > prev {
				out.ThrottledMask |= 1 << uint(i)
			}
		}
	}

	s.prevThrottle = curThrottle
}

// sampleSensors reads every hwmon temp*_input file under /sys/class/hwmon,
// in a stable (directory-then-filename) order — hwmon numbering isn't
// guaranteed stable across reboots, but within one recording session it's
// at least consistent sample to sample, which is what matters for the
// black box's own trend data.
func (s *Sampler) sampleSensors(out *Sample) {
	hwmonRoot := filepath.Join(s.SysRoot, "class", "hwmon")
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		return
	}

	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)

	n := 0
	for _, dir := range dirs {
		if n >= MaxSensors {
			break
		}
		devicePath := filepath.Join(hwmonRoot, dir)
		files, err := os.ReadDir(devicePath)
		if err != nil {
			continue
		}

		var inputs []string
		for _, f := range files {
			if strings.HasPrefix(f.Name(), "temp") && strings.HasSuffix(f.Name(), "_input") {
				inputs = append(inputs, f.Name())
			}
		}
		sort.Strings(inputs)

		for _, in := range inputs {
			if n >= MaxSensors {
				break
			}
			milliC, err := readIntFile(filepath.Join(devicePath, in))
			if err != nil {
				continue
			}
			out.SensorTempMilliC[n] = int32(milliC)
			n++
		}
	}
	out.NumSensors = uint16(n)
}

// sampleEDAC reads ECC error counters from every memory controller under
// /sys/devices/system/edac/mc, summed, and reports the delta since the
// previous sample.
func (s *Sampler) sampleEDAC(out *Sample) {
	mcRoot := filepath.Join(s.SysRoot, "devices", "system", "edac", "mc")
	entries, err := os.ReadDir(mcRoot)
	if err != nil {
		return
	}

	var ce, ue uint64
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "mc") {
			continue
		}
		mcPath := filepath.Join(mcRoot, e.Name())
		if v, err := readUintFile(filepath.Join(mcPath, "ce_count")); err == nil {
			ce += v
			found = true
		}
		if v, err := readUintFile(filepath.Join(mcPath, "ue_count")); err == nil {
			ue += v
			found = true
		}
	}
	if !found {
		return
	}

	if !s.prevAt.IsZero() {
		out.EDACCorrectableDelta = deltaUint64(s.prevEDACCe, ce)
		out.EDACUncorrectableDelta = deltaUint64(s.prevEDACUe, ue)
	}
	s.prevEDACCe = ce
	s.prevEDACUe = ue
}

func deltaUint64(prev, cur uint64) uint64 {
	if cur < prev {
		return 0 // counter reset (module reload, reboot) — never a negative delta
	}
	return cur - prev
}

func readUintFile(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}

func readIntFile(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}
