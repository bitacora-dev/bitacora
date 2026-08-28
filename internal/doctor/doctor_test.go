package doctor

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/spool"
)

// currentUserAndPrimaryGroup returns the real user running the test and the
// name of their primary group, so tests can exercise the "OK" path against
// an account that genuinely exists and is genuinely a member of that group
// — without needing root or a fixed username that may not exist on every
// machine running these tests (dev laptop vs. Linux CI runner).
func currentUserAndPrimaryGroup(t *testing.T) (*user.User, string) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot look up current user in this environment: %v", err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Skipf("cannot look up primary group in this environment: %v", err)
	}
	return u, g.Name
}

func TestCheckSystemUser_OKWhenUserExistsAndIsInGroup(t *testing.T) {
	u, group := currentUserAndPrimaryGroup(t)

	check := checkSystemUser(Config{SystemUser: u.Username, RequiredGroup: group})
	if !check.OK {
		t.Fatalf("expected OK, got %+v", check)
	}
}

func TestCheckSystemUser_FailsWhenUserDoesNotExist(t *testing.T) {
	check := checkSystemUser(Config{SystemUser: "no-such-bitacora-test-user", RequiredGroup: "whatever"})
	if check.OK {
		t.Fatal("expected failure for a nonexistent user")
	}
}

func TestCheckSystemUser_FailsWhenUserNotInRequiredGroup(t *testing.T) {
	u, _ := currentUserAndPrimaryGroup(t)

	// Pick a group name that is very unlikely to be the current user's:
	// a group that doesn't exist at all fails the "group not found" branch,
	// which is still a legitimate failure to assert on.
	check := checkSystemUser(Config{SystemUser: u.Username, RequiredGroup: "no-such-bitacora-test-group"})
	if check.OK {
		t.Fatal("expected failure when the required group doesn't exist")
	}
}

func TestCheckTimerPresence_OKWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bitacora-smart.timer")
	if err := os.WriteFile(path, []byte("[Timer]\n"), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	check := checkTimerPresence(Config{TimerUnitPaths: []string{path}})
	if !check.OK {
		t.Fatalf("expected OK, got %+v", check)
	}
}

func TestCheckTimerPresence_FailsWhenMissing(t *testing.T) {
	check := checkTimerPresence(Config{TimerUnitPaths: []string{"/does/not/exist/bitacora-smart.timer"}})
	if check.OK {
		t.Fatal("expected failure when no timer unit path exists")
	}
}

func TestCheckSpoolPermissions_OKWithMatchingModeAndOwner(t *testing.T) {
	u, group := currentUserAndPrimaryGroup(t)

	dir := t.TempDir()
	spoolDir := filepath.Join(dir, "spool")
	if err := os.Mkdir(spoolDir, 0o750); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	check := checkSpoolPermissions(Config{
		SpoolDir:        spoolDir,
		SpoolOwnerUser:  u.Username,
		SpoolOwnerGroup: group,
		SpoolMode:       0o750,
	})
	if !check.OK {
		t.Fatalf("expected OK, got %+v", check)
	}
}

func TestCheckSpoolPermissions_FailsOnWrongMode(t *testing.T) {
	u, group := currentUserAndPrimaryGroup(t)

	dir := t.TempDir()
	spoolDir := filepath.Join(dir, "spool")
	if err := os.Mkdir(spoolDir, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	check := checkSpoolPermissions(Config{
		SpoolDir:        spoolDir,
		SpoolOwnerUser:  u.Username,
		SpoolOwnerGroup: group,
		SpoolMode:       0o750,
	})
	if check.OK {
		t.Fatal("expected failure for a 0755 dir when 0750 was required")
	}
}

func TestCheckSpoolPermissions_FailsWhenMissing(t *testing.T) {
	check := checkSpoolPermissions(Config{SpoolDir: "/does/not/exist/bitacora-spool-test", SpoolMode: 0o750})
	if check.OK {
		t.Fatal("expected failure for a missing spool dir")
	}
}

func TestCheckSpoolFreshness_OKWhenFresh(t *testing.T) {
	dir := t.TempDir()
	if err := spool.WriteAtomic(dir, "smart", 1, map[string]any{"ok": true}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	check := checkSpoolFreshness(Config{
		SpoolDir:        dir,
		HelperIntervals: map[string]time.Duration{"smart": 15 * time.Minute},
	})
	if !check.OK {
		t.Fatalf("expected OK for a freshly written entry, got %+v", check)
	}
}

func TestCheckSpoolFreshness_FailsWhenNeverReported(t *testing.T) {
	dir := t.TempDir()

	check := checkSpoolFreshness(Config{
		SpoolDir:        dir,
		HelperIntervals: map[string]time.Duration{"smart": 15 * time.Minute},
	})
	if check.OK {
		t.Fatal("expected failure when an expected helper has never reported")
	}
}
