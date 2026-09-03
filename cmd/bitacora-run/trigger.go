// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strconv"
	"strings"
)

// detectTrigger reports how bitacora-run was invoked (ADR-0010: "Registra
// inicio, con trigger detectado (systemd, cron, manual)"), best-effort.
func detectTrigger() string {
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("JOURNAL_STREAM") != "" {
		return "systemd"
	}
	if isCronParent(os.Getppid()) {
		return "cron"
	}
	return "manual"
}

// isCronParent reports whether ppid's command name looks like cron —
// Linux-only (reads /proc), harmlessly false everywhere else. There's no
// portable, dependency-free way to name an arbitrary parent process, and
// pulling in one just for this would work against bitacora-run's
// "small, no dependencies" requirement.
func isCronParent(ppid int) bool {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/comm")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(string(comm))), "cron")
}
