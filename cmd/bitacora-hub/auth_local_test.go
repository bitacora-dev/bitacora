package main

import (
	"io"
	"os"
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
