package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

func TestExistingLocalAuthCommandsRequireCurrentPasswordBeforeMutation(t *testing.T) {
	commands := []string{"rotate-password", "rotate-totp", "regenerate-recovery-codes", "disable"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			store := hubauth.NewLocalStore(filepath.Join(dir, "local-auth.json"), filepath.Join(dir, "local-auth.key"))
			_, recovery, err := store.Initialize("current-password")
			if err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			prompted := 0
			dependencies := localAuthCommandDependencies{
				newStore:  func() *hubauth.LocalStore { return store },
				openStore: func() (*hubauth.LocalStore, error) { return store, nil },
				promptNew: func(_ *os.File, _ io.Writer) (string, error) {
					t.Fatal("new-password prompt must not run after a failed current-password check")
					return "", nil
				},
				promptCurrent: func(_ *os.File, _ io.Writer) (string, error) {
					prompted++
					return "wrong-password", nil
				},
				ensureOwner: func() error {
					t.Fatal("ownership must not change after a failed current-password check")
					return nil
				},
			}
			err = runLocalAuthCommandWithDependencies([]string{"local", command}, nil, io.Discard, dependencies)
			if err == nil || !strings.Contains(err.Error(), "current local password is invalid") {
				t.Fatalf("command error = %v, want failed current-password check", err)
			}
			if prompted != 1 {
				t.Fatalf("current password prompt count = %d, want 1", prompted)
			}
			if command == "disable" && !store.IsEnabled() {
				t.Fatal("failed password check disabled local authentication")
			}
			if command != "disable" {
				if _, err := store.Authenticate("current-password", recovery[0]); err != nil {
					t.Fatalf("failed password check changed second-factor material: %v", err)
				}
			}
		})
	}
}

func TestDisableVerifiesCurrentPasswordBeforeDisabling(t *testing.T) {
	dir := t.TempDir()
	store := hubauth.NewLocalStore(filepath.Join(dir, "local-auth.json"), filepath.Join(dir, "local-auth.key"))
	if _, _, err := store.Initialize("current-password"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	prompted := 0
	err := runLocalAuthCommandWithDependencies([]string{"local", "disable"}, nil, io.Discard, localAuthCommandDependencies{
		newStore:  func() *hubauth.LocalStore { return store },
		openStore: func() (*hubauth.LocalStore, error) { return store, nil },
		promptNew: func(*os.File, io.Writer) (string, error) { return "", nil },
		promptCurrent: func(*os.File, io.Writer) (string, error) {
			prompted++
			return "current-password", nil
		},
		ensureOwner: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if prompted != 1 {
		t.Fatalf("current password prompt count = %d, want 1", prompted)
	}
	if store.IsEnabled() {
		t.Fatal("verified current password did not disable local authentication")
	}
}

func TestInitPrintsSecondFactorBeforeOwnershipFailure(t *testing.T) {
	dir := t.TempDir()
	store := hubauth.NewLocalStore(filepath.Join(dir, "local-auth.json"), filepath.Join(dir, "local-auth.key"))
	var output strings.Builder
	err := runLocalAuthCommandWithDependencies([]string{"local", "init"}, nil, &output, localAuthCommandDependencies{
		newStore:    func() *hubauth.LocalStore { return store },
		promptNew:   func(*os.File, io.Writer) (string, error) { return "password", nil },
		ensureOwner: func() error { return errors.New("simulated ownership failure") },
	})
	if err == nil || !strings.Contains(err.Error(), "simulated ownership failure") {
		t.Fatalf("init error = %v, want ownership failure", err)
	}
	if !strings.Contains(output.String(), "TOTP secret (shown once):") || !strings.Contains(output.String(), "Recovery codes (shown once; store them offline):") {
		t.Fatalf("init output did not preserve second-factor material: %s", output.String())
	}
	if _, err := store.Authenticate("password", recoveryCodeFromOutput(t, output.String())); err != nil {
		t.Fatalf("credential left unusable after ownership failure: %v", err)
	}
}

func TestEnsureLocalAuthOwnerInContainerWithoutSystemUserKeepsPrivateModes(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "local-auth.json"), filepath.Join(dir, "local-auth.key")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("credential"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod %s: %v", path, err)
		}
	}
	var warnings []string
	err := ensureLocalAuthOwnerForPaths(paths, localAuthOwnerDependencies{
		euid:   func() int { return 0 },
		lookup: func(string) (*user.User, error) { return nil, user.UnknownUserError("bitacora") },
		chown: func(string, int, int) error {
			t.Fatal("container path must not chown without bitacora user")
			return nil
		},
		chmod:       os.Chmod,
		isContainer: func() bool { return true },
		warn:        func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("ensure owner in container: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one explicit container warning", warnings)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 0600", path, got)
		}
	}
}

func TestEnsureLocalAuthOwnerForSystemdInstallationChownsBothFiles(t *testing.T) {
	paths := []string{"state", "key"}
	var chowned []string
	var chmodded []string
	err := ensureLocalAuthOwnerForPaths(paths, localAuthOwnerDependencies{
		euid:   func() int { return 0 },
		lookup: func(string) (*user.User, error) { return &user.User{Uid: "1001", Gid: "1002"}, nil },
		chown: func(path string, uid, gid int) error {
			chowned = append(chowned, fmt.Sprintf("%s:%d:%d", path, uid, gid))
			return nil
		},
		chmod: func(path string, mode os.FileMode) error {
			chmodded = append(chmodded, fmt.Sprintf("%s:%o", path, mode))
			return nil
		},
		isContainer: func() bool { return false },
		warn:        func(string, ...any) { t.Fatal("systemd path must not warn") },
	})
	if err != nil {
		t.Fatalf("ensure owner for systemd installation: %v", err)
	}
	if got, want := strings.Join(chowned, ","), "state:1001:1002,key:1001:1002"; got != want {
		t.Errorf("chown calls = %q, want %q", got, want)
	}
	if got, want := strings.Join(chmodded, ","), "state:600,key:600"; got != want {
		t.Errorf("chmod calls = %q, want %q", got, want)
	}
}

func recoveryCodeFromOutput(t *testing.T, output string) string {
	t.Helper()
	lines := strings.Split(output, "\n")
	for index, line := range lines {
		if line == "Recovery codes (shown once; store them offline):" && index+1 < len(lines) {
			return lines[index+1]
		}
	}
	t.Fatal("recovery code not found in output")
	return ""
}
