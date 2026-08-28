package doctor

import (
	"fmt"
	"os"
	"os/user"
	"syscall"
	"time"

	"github.com/bitacora-dev/bitacora/internal/spool"
)

func checkSpoolPermissions(cfg Config) Check {
	name := fmt.Sprintf("spool dir %s", cfg.SpoolDir)

	info, err := os.Stat(cfg.SpoolDir)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}
	if !info.IsDir() {
		return Check{Name: name, OK: false, Detail: "exists but is not a directory"}
	}

	if got := info.Mode().Perm(); got != cfg.SpoolMode {
		return Check{Name: name, OK: false, Detail: fmt.Sprintf("mode is %o, expected %o", got, cfg.SpoolMode)}
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Check{Name: name, OK: false, Detail: "could not read owner/group (unsupported platform)"}
	}

	wantUser, err := user.Lookup(cfg.SpoolOwnerUser)
	if err != nil {
		return Check{Name: name, OK: false, Detail: fmt.Sprintf("expected owner %q not found: %v", cfg.SpoolOwnerUser, err)}
	}
	wantGroup, err := user.LookupGroup(cfg.SpoolOwnerGroup)
	if err != nil {
		return Check{Name: name, OK: false, Detail: fmt.Sprintf("expected group %q not found: %v", cfg.SpoolOwnerGroup, err)}
	}

	gotUID := fmt.Sprintf("%d", stat.Uid)
	gotGID := fmt.Sprintf("%d", stat.Gid)
	if gotUID != wantUser.Uid || gotGID != wantGroup.Gid {
		return Check{
			Name: name, OK: false,
			Detail: fmt.Sprintf("owned by uid=%s gid=%s, expected %s:%s (uid=%s gid=%s)",
				gotUID, gotGID, cfg.SpoolOwnerUser, cfg.SpoolOwnerGroup, wantUser.Uid, wantGroup.Gid),
		}
	}

	return Check{Name: name, OK: true, Detail: fmt.Sprintf("mode %o, owned by %s:%s", cfg.SpoolMode, cfg.SpoolOwnerUser, cfg.SpoolOwnerGroup)}
}

func checkSpoolFreshness(cfg Config) Check {
	name := "spool freshness"

	entries, err := spool.ReadDir(cfg.SpoolDir)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}

	if len(cfg.HelperIntervals) == 0 {
		return Check{Name: name, OK: true, Detail: "no helper intervals configured to check against"}
	}

	now := time.Now()
	var stale []string
	var missing []string

	for collector, interval := range cfg.HelperIntervals {
		entry, ok := entries[collector]
		if !ok {
			missing = append(missing, collector)
			continue
		}
		if entry.Stale(now, interval) {
			stale = append(stale, fmt.Sprintf("%s (last run %s ago, expected every %s)", collector, entry.Age(now).Round(time.Second), interval))
		}
	}

	if len(stale) == 0 && len(missing) == 0 {
		return Check{Name: name, OK: true, Detail: "all known helpers reported within 3x their interval"}
	}

	detail := ""
	if len(stale) > 0 {
		detail += fmt.Sprintf("stale: %v", stale)
	}
	if len(missing) > 0 {
		if detail != "" {
			detail += "; "
		}
		detail += fmt.Sprintf("never reported: %v", missing)
	}
	return Check{Name: name, OK: false, Detail: detail}
}
