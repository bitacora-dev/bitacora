package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"

	"golang.org/x/term"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// runLocalAuthCommand deliberately accepts no credential arguments. The only
// place a password enters this process is a terminal with echo disabled.
func runLocalAuthCommand(args []string, in *os.File, out io.Writer) error {
	return runLocalAuthCommandWithDependencies(args, in, out, localAuthCommandDependencies{
		newStore: func() *hubauth.LocalStore {
			return hubauth.NewLocalStore(hubauth.DefaultLocalAuthPath, hubauth.DefaultLocalAuthKeyPath)
		},
		openStore: func() (*hubauth.LocalStore, error) {
			return hubauth.OpenLocalStore(hubauth.DefaultLocalAuthPath, hubauth.DefaultLocalAuthKeyPath)
		},
		promptNew:     promptNewPassword,
		promptCurrent: promptCurrentPassword,
		ensureOwner:   ensureLocalAuthOwner,
	})
}

type localAuthCommandDependencies struct {
	newStore      func() *hubauth.LocalStore
	openStore     func() (*hubauth.LocalStore, error)
	promptNew     func(*os.File, io.Writer) (string, error)
	promptCurrent func(*os.File, io.Writer) (string, error)
	ensureOwner   func() error
}

func runLocalAuthCommandWithDependencies(args []string, in *os.File, out io.Writer, dependencies localAuthCommandDependencies) error {
	if len(args) != 2 || args[0] != "local" {
		return errors.New("usage: bitacora-hub auth local <init|rotate-password|rotate-totp|regenerate-recovery-codes|disable>")
	}
	command := args[1]
	store := dependencies.newStore()
	if command != "init" {
		opened, err := dependencies.openStore()
		if err != nil {
			return err
		}
		if opened == nil {
			return hubauth.ErrLocalAuthNotConfigured
		}
		store = opened
	}

	switch command {
	case "init":
		password, err := dependencies.promptNew(in, out)
		if err != nil {
			return err
		}
		secret, recovery, err := store.Initialize(password)
		if err != nil {
			return err
		}
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
		printSecondFactor(out, secret, recovery)
	case "rotate-password":
		if err := verifyCurrentPassword(store, in, out, dependencies.promptCurrent); err != nil {
			return err
		}
		password, err := dependencies.promptNew(in, out)
		if err != nil {
			return err
		}
		if err := store.RotatePassword(password); err != nil {
			return err
		}
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Local password rotated; existing local sessions were revoked.")
	case "rotate-totp":
		if err := verifyCurrentPassword(store, in, out, dependencies.promptCurrent); err != nil {
			return err
		}
		secret, recovery, err := store.RotateTOTP()
		if err != nil {
			return err
		}
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
		printSecondFactor(out, secret, recovery)
	case "regenerate-recovery-codes":
		if err := verifyCurrentPassword(store, in, out, dependencies.promptCurrent); err != nil {
			return err
		}
		recovery, err := store.RegenerateRecoveryCodes()
		if err != nil {
			return err
		}
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Existing local sessions were revoked.")
		printRecoveryCodes(out, recovery)
	case "disable":
		if err := verifyCurrentPassword(store, in, out, dependencies.promptCurrent); err != nil {
			return err
		}
		if err := store.Disable(); err != nil {
			return err
		}
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Local authentication disabled; existing local sessions were revoked.")
	default:
		return errors.New("usage: bitacora-hub auth local <init|rotate-password|rotate-totp|regenerate-recovery-codes|disable>")
	}
	return nil
}

func promptNewPassword(in *os.File, out io.Writer) (string, error) {
	password, err := readPassword(in, out, "New local password: ")
	if err != nil {
		return "", err
	}
	confirmation, err := readPassword(in, out, "Confirm local password: ")
	if err != nil {
		return "", err
	}
	if password != confirmation {
		return "", errors.New("password confirmation does not match")
	}
	return password, nil
}

func promptCurrentPassword(in *os.File, out io.Writer) (string, error) {
	return readPassword(in, out, "Current local password: ")
}

func verifyCurrentPassword(store *hubauth.LocalStore, in *os.File, out io.Writer, prompt func(*os.File, io.Writer) (string, error)) error {
	password, err := prompt(in, out)
	if err != nil {
		return err
	}
	if err := store.VerifyPassword(password); err != nil {
		return errors.New("current local password is invalid")
	}
	return nil
}

func readPassword(in *os.File, out io.Writer, prompt string) (string, error) {
	if in == nil || !term.IsTerminal(int(in.Fd())) {
		return "", errors.New("local authentication passwords must be entered through a TTY")
	}
	fmt.Fprint(out, prompt)
	value, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("reading password from TTY: %w", err)
	}
	return string(value), nil
}

func printSecondFactor(out io.Writer, secret string, recovery []string) {
	fmt.Fprintln(out, "TOTP secret (shown once):", secret)
	printRecoveryCodes(out, recovery)
}

func printRecoveryCodes(out io.Writer, recovery []string) {
	fmt.Fprintln(out, "Recovery codes (shown once; store them offline):")
	for _, code := range recovery {
		fmt.Fprintln(out, code)
	}
}

// Packaging creates the bitacora system user. Restrict ownership here, where
// the fixed /etc paths are used, without making temp-directory unit tests need
// a host-specific account.
func ensureLocalAuthOwner() error {
	if os.Geteuid() != 0 {
		return nil
	}
	account, err := user.Lookup("bitacora")
	if err != nil {
		return fmt.Errorf("looking up bitacora owner: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parsing bitacora uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parsing bitacora gid: %w", err)
	}
	for _, path := range []string{hubauth.DefaultLocalAuthPath, hubauth.DefaultLocalAuthKeyPath} {
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("setting owner for %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("setting mode for %s: %w", path, err)
		}
	}
	return nil
}
