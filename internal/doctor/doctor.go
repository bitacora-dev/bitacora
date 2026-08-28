// Package doctor implements the checks behind `bita doctor` (ADR-0005):
// system user and group membership, spool directory permissions, helper
// timer presence, and spool freshness. It never shells out — everything is
// a filesystem or os/user lookup, consistent with ADR-0012 (bita is not a
// helper, so it doesn't get to use os/exec).
package doctor

import (
	"fmt"
	"os"
	"os/user"
	"time"
)

// Config is everything doctor.Run needs to know about the expected
// installation, so it's testable against a fake root instead of the real
// system.
type Config struct {
	SystemUser      string
	RequiredGroup   string
	SpoolDir        string
	SpoolOwnerUser  string
	SpoolOwnerGroup string
	SpoolMode       os.FileMode
	TimerUnitPaths  []string
	HelperIntervals map[string]time.Duration
}

// DefaultConfig is the real, production installation this project ships
// (ADR-0005).
var DefaultConfig = Config{
	SystemUser:      "bitacora",
	RequiredGroup:   "systemd-journal",
	SpoolDir:        "/var/lib/bitacora/spool",
	SpoolOwnerUser:  "root",
	SpoolOwnerGroup: "bitacora",
	SpoolMode:       0o750,
	TimerUnitPaths: []string{
		"/etc/systemd/system/bitacora-smart.timer",
		"/usr/lib/systemd/system/bitacora-smart.timer",
	},
	HelperIntervals: map[string]time.Duration{
		"smart": 15 * time.Minute,
	},
}

// Check is one doctor finding.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Run executes every check against cfg and returns them in a stable order.
func Run(cfg Config) []Check {
	return []Check{
		checkSystemUser(cfg),
		checkSpoolPermissions(cfg),
		checkTimerPresence(cfg),
		checkSpoolFreshness(cfg),
	}
}

func checkSystemUser(cfg Config) Check {
	name := fmt.Sprintf("system user %q", cfg.SystemUser)

	u, err := user.Lookup(cfg.SystemUser)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}

	group, err := user.LookupGroup(cfg.RequiredGroup)
	if err != nil {
		return Check{Name: name, OK: false, Detail: fmt.Sprintf("user exists but required group %q was not found: %v", cfg.RequiredGroup, err)}
	}

	gids, err := u.GroupIds()
	if err != nil {
		return Check{Name: name, OK: false, Detail: fmt.Sprintf("user exists but its groups could not be listed: %v", err)}
	}

	for _, gid := range gids {
		if gid == group.Gid {
			return Check{Name: name, OK: true, Detail: fmt.Sprintf("exists, member of %q", cfg.RequiredGroup)}
		}
	}
	return Check{Name: name, OK: false, Detail: fmt.Sprintf("exists but is not a member of required group %q", cfg.RequiredGroup)}
}

func checkTimerPresence(cfg Config) Check {
	name := "helper timer"
	for _, path := range cfg.TimerUnitPaths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return Check{Name: name, OK: true, Detail: fmt.Sprintf("found at %s", path)}
		}
	}
	return Check{Name: name, OK: false, Detail: fmt.Sprintf("not found in any of %v", cfg.TimerUnitPaths)}
}
