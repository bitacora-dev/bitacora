// bitacora-smart is a short-lived, root, timer-triggered helper (ADR-0005):
// it reads S.M.A.R.T. data for every block device and writes one spool
// entry, then exits. It never keeps state, never opens a network socket
// (PrivateNetwork=yes in its unit), and its exec target — smartctl — is
// only ever called with a device name sourced from enumerating /sys/block,
// per the one exception ADR-0012 carves out for helpers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/smarthelper"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

const (
	sysBlockDir    = "/sys/block"
	spoolDir       = "/var/lib/bitacora/spool"
	overallLimit   = 45 * time.Second
	perDeviceLimit = 10 * time.Second
)

// virtualDevicePrefixes are /sys/block entries that are never a physical
// disk worth SMART-querying.
var virtualDevicePrefixes = []string{"loop", "ram", "sr", "dm-", "md", "zram"}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), overallLimit)
	defer cancel()

	result, errs := smarthelper.Run(ctx, listSysBlockDevices, runSmartctl)

	if err := spool.WriteAtomic(spoolDir, "smart", 1, result, errs); err != nil {
		fmt.Fprintln(os.Stderr, "bitacora-smart: writing spool entry:", err)
		os.Exit(1)
	}

	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "bitacora-smart: completed with %d device error(s)\n", len(errs))
	}
}

func listSysBlockDevices() ([]string, error) {
	entries, err := os.ReadDir(sysBlockDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sysBlockDir, err)
	}

	var devices []string
	for _, e := range entries {
		name := e.Name()
		if isVirtualDevice(name) {
			continue
		}
		devices = append(devices, name)
	}
	return devices, nil
}

func isVirtualDevice(name string) bool {
	for _, prefix := range virtualDevicePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func runSmartctl(ctx context.Context, device string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, perDeviceLimit)
	defer cancel()

	out, err := exec.CommandContext(cctx, "smartctl", "--json", "-a", "/dev/"+device).Output()
	if err != nil {
		// smartctl's exit code encodes device health bits, not just
		// success/failure — it can legitimately exit non-zero while still
		// producing usable JSON on stdout. If we got output, prefer it.
		if len(out) > 0 {
			return out, nil
		}
		return nil, err
	}
	return out, nil
}
