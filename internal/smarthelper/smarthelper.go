// Package smarthelper implements the bitacora-smart helper's logic
// (ADR-0005): read S.M.A.R.T. data for every block device and hand it back
// for the caller to write to the spool. Device listing and command
// execution are injected so this is testable without real disks or a real
// smartctl binary.
package smarthelper

import (
	"context"
	"encoding/json"
	"fmt"
)

// DeviceLister returns the block device names to query (e.g. "sda",
// "nvme0n1"), enumerated from /sys/block — never from a request or any
// externally supplied value (ADR-0012: only device names sourced from
// enumeration are an acceptable exec argument).
type DeviceLister func() ([]string, error)

// CommandRunner runs `smartctl --json -a /dev/<device>` for one device and
// returns its raw JSON stdout.
type CommandRunner func(ctx context.Context, device string) ([]byte, error)

// Result is the "data" payload written into the spool entry.
type Result struct {
	Devices map[string]json.RawMessage `json:"devices"`
}

// Run lists devices and queries each one. A failure on a single device is
// collected as a non-fatal error, not a reason to abort the rest — one dead
// or timing-out disk shouldn't blind the report on every other one.
func Run(ctx context.Context, list DeviceLister, run CommandRunner) (Result, []string) {
	devices, err := list()
	if err != nil {
		return Result{Devices: map[string]json.RawMessage{}}, []string{fmt.Sprintf("listing devices: %v", err)}
	}

	result := Result{Devices: make(map[string]json.RawMessage, len(devices))}
	var errs []string

	for _, device := range devices {
		out, err := run(ctx, device)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", device, err))
			continue
		}
		if !json.Valid(out) {
			errs = append(errs, fmt.Sprintf("%s: smartctl did not return valid JSON", device))
			continue
		}
		result.Devices[device] = json.RawMessage(out)
	}

	return result, errs
}
