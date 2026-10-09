// Package hwmon reads Linux hwmon temperature inputs.
package hwmon

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Temperature is one readable tempN_input entry. Chip and Label come from
// hwmon's device-provided names; Input is retained for a stable fallback when
// a driver does not provide a tempN_label file.
type Temperature struct {
	Chip   string
	Label  string
	Input  string
	MilliC int64
}

// ReadTemperatures reads every readable hwmon tempN_input beneath root. Its
// directory-then-filename ordering is intentional: the black box uses it to
// preserve the order of its fixed-size sensor array between samples.
func ReadTemperatures(root string) ([]Temperature, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		dirs = append(dirs, entry.Name())
	}
	sort.Strings(dirs)

	var temperatures []Temperature
	for _, dir := range dirs {
		devicePath := filepath.Join(root, dir)
		files, err := os.ReadDir(devicePath)
		if err != nil {
			continue
		}

		inputs := make([]string, 0, len(files))
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "temp") && strings.HasSuffix(file.Name(), "_input") {
				inputs = append(inputs, file.Name())
			}
		}
		sort.Strings(inputs)

		chip := readTrimmed(filepath.Join(devicePath, "name"))
		for _, input := range inputs {
			milliC, err := readMilliC(filepath.Join(devicePath, input))
			if err != nil {
				continue
			}
			labelFile := strings.TrimSuffix(input, "_input") + "_label"
			temperatures = append(temperatures, Temperature{
				Chip:   chip,
				Label:  readTrimmed(filepath.Join(devicePath, labelFile)),
				Input:  strings.TrimSuffix(input, "_input"),
				MilliC: milliC,
			})
		}
	}
	return temperatures, nil
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func readMilliC(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}
