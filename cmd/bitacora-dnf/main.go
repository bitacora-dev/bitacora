// bitacora-dnf is a short-lived, root, timer-triggered helper
// (ADR-0005, ADR-0017): it runs `dnf check-update` — a closed query with
// no parameters sourced from anything external, exactly the kind of
// command ADR-0012 permits a helper to exec — and writes one spool
// entry, then exits. Unlike bitacora-smart and bitacora-vpn it
// legitimately needs network egress: checking for updates means querying
// the host's configured repository mirrors, not reading local state.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/bitacora-dev/bitacora/internal/dnfhelper"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

const (
	spoolDir     = "/var/lib/bitacora/spool"
	overallLimit = 2 * time.Minute
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), overallLimit)
	defer cancel()

	result, errs := dnfhelper.Run(ctx, runCheckUpdate)

	if err := spool.WriteAtomic(spoolDir, "dnfupdates", 1, result, errs); err != nil {
		fmt.Fprintln(os.Stderr, "bitacora-dnf: writing spool entry:", err)
		os.Exit(1)
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "bitacora-dnf: completed with error(s): %v\n", errs)
	}
}

// runCheckUpdate calls the one command this helper ever calls, with no
// arguments — dnf's own exit code 100 means "updates found", which
// exec.Cmd surfaces as a non-nil *exec.ExitError even though it isn't a
// failure worth reporting.
func runCheckUpdate(ctx context.Context) ([]byte, bool, error) {
	out, err := exec.CommandContext(ctx, "dnf", "check-update").Output()
	if err == nil {
		return out, false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 100 {
		return out, true, nil
	}
	return out, false, err
}
