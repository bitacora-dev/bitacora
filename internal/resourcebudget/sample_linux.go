//go:build linux

package resourcebudget

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// clockTicksPerSecond is USER_HZ, the unit /proc/[pid]/stat's utime/stime
// fields are expressed in. 100 on virtually every Linux distribution.
const clockTicksPerSecond = 100

// Sample reads /proc/<pid>/status and /proc/<pid>/stat for a point-in-time
// RSS and cumulative CPU time. cpuSeconds is total user+system CPU time
// consumed since the process started, not a rate — a caller wanting a
// fraction ("2% of one core") needs two samples and a wall-clock delta.
func Sample(pid int) (rssBytes uint64, cpuSeconds float64, err error) {
	rssBytes, err = readRSS(pid)
	if err != nil {
		return 0, 0, err
	}
	cpuSeconds, err = readCPUSeconds(pid)
	if err != nil {
		return 0, 0, err
	}
	return rssBytes, cpuSeconds, nil
}

func readRSS(pid int) (uint64, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected VmRSS line: %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing VmRSS: %w", err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("VmRSS not found in /proc/%d/status", pid)
}

func readCPUSeconds(pid int) (float64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	// The process name field is parenthesized and may itself contain
	// spaces or parens, so the reliable split point is the LAST ')' in the
	// line, per proc(5).
	s := string(raw)
	idx := strings.LastIndex(s, ")")
	if idx == -1 {
		return 0, fmt.Errorf("unexpected /proc/%d/stat format", pid)
	}

	// Fields after "comm)" start at field 3 (state). utime is field 14,
	// stime is field 15, i.e. indexes 11 and 12 in this zero-based
	// remainder slice.
	fields := strings.Fields(s[idx+1:])
	if len(fields) < 13 {
		return 0, fmt.Errorf("unexpected /proc/%d/stat field count: %d", pid, len(fields))
	}

	utime, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing utime: %w", err)
	}
	stime, err := strconv.ParseFloat(fields[12], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing stime: %w", err)
	}

	return (utime + stime) / clockTicksPerSecond, nil
}
