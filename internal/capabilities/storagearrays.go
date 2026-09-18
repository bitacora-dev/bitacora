package capabilities

import (
	"bufio"
	"strconv"
	"strings"
)

// MDArray describes one active array reported by /proc/mdstat.
type MDArray struct {
	Name        string
	Level       string
	MemberCount int
	Degraded    bool
}

// ParseMDStat reads the active-array entries in /proc/mdstat. It only uses
// data exposed by the kernel's read-only status file.
func ParseMDStat(data []byte) []MDArray {
	var arrays []MDArray
	var current *MDArray
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "md") {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[1] != ":" || fields[2] != "active" {
				current = nil
				continue
			}
			array := MDArray{Name: fields[0], Level: fields[3]}
			for _, field := range fields[4:] {
				if strings.Contains(field, "[") && strings.Contains(field, "]") {
					array.MemberCount++
				}
			}
			arrays = append(arrays, array)
			current = &arrays[len(arrays)-1]
			continue
		}

		if current == nil {
			continue
		}
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "[") || !strings.HasSuffix(field, "]") {
				continue
			}
			status := strings.Trim(field, "[]")
			if strings.Contains(status, "/") {
				parts := strings.SplitN(status, "/", 2)
				expected, expectedErr := strconv.Atoi(parts[0])
				active, activeErr := strconv.Atoi(parts[1])
				if expectedErr == nil && activeErr == nil && active < expected {
					current.Degraded = true
				}
			} else if strings.Contains(status, "_") {
				current.Degraded = true
			}
		}
	}
	return arrays
}

// SnapraidArray describes the data and parity locations declared in a
// SnapRAID configuration file. It intentionally does not claim a health
// state: that requires a SnapRAID sync/status run, which ADR-0012 forbids.
type SnapraidArray struct {
	Locations   []string
	ParityDisks int
}

// ParseSnapraidConfig extracts data and parity locations from snapraid.conf.
func ParseSnapraidConfig(data []byte) SnapraidArray {
	var array SnapraidArray
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "data" && fields[0] != "parity") {
			continue
		}
		array.Locations = append(array.Locations, fields[len(fields)-1])
		if fields[0] == "parity" {
			array.ParityDisks++
		}
	}
	return array
}
