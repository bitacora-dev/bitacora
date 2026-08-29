// bitacora-vpn is a short-lived, root, timer-triggered helper (ADR-0005):
// it reads WireGuard and Tailscale tunnel status and writes one spool
// entry, then exits. Both commands it calls are a closed list with no
// arguments derived from anything external (ADR-0012).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/bitacora-dev/bitacora/internal/spool"
	"github.com/bitacora-dev/bitacora/internal/vpnhelper"
)

const (
	spoolDir     = "/var/lib/bitacora/spool"
	overallLimit = 15 * time.Second
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), overallLimit)
	defer cancel()

	result, errs := vpnhelper.Run(ctx, runCommand)

	if err := spool.WriteAtomic(spoolDir, "vpn", 1, result, errs); err != nil {
		fmt.Fprintln(os.Stderr, "bitacora-vpn: writing spool entry:", err)
		os.Exit(1)
	}

	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "bitacora-vpn: completed with %d error(s)\n", len(errs))
	}
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
