package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"strconv"

	"golang.org/x/term"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// runLocalAuthCommand deliberately accepts no credential arguments. The only
// place a password enters this process is a terminal with echo disabled.
func runLocalAuthCommand(args []string, in *os.File, out io.Writer) error {
	// The same configuration the server reads. Resolving the paths twice from
	// different sources is how you end up initializing a credential the hub
	// will never look at.
	config, err := hubauth.LocalConfigFromEnv()
	if err != nil {
		return err
	}
	path, keyPath := config.StatePath(), config.KeyStatePath()
	return runLocalAuthCommandWithDependencies(args, in, out, localAuthCommandDependencies{
		newStore: func() *hubauth.LocalStore {
			return hubauth.NewLocalStore(path, keyPath)
		},
		openStore: func() (*hubauth.LocalStore, error) {
			return hubauth.OpenLocalStore(path, keyPath)
		},
		promptNew:     promptNewPassword,
		promptCurrent: promptCurrentPassword,
		ensureOwner:   func() error { return ensureLocalAuthOwner(path, keyPath) },
		ensureSetupOwner: func() error {
			return ensureLocalAuthOwner(hubauth.NewLocalStore(path, keyPath).SetupTokenPath())
		},
	})
}

const localAuthUsage = "usage: bitacora-hub auth local <init|rotate-password|rotate-totp|regenerate-recovery-codes|disable|recover>"

type localAuthCommandDependencies struct {
	newStore      func() *hubauth.LocalStore
	openStore     func() (*hubauth.LocalStore, error)
	promptNew     func(*os.File, io.Writer) (string, error)
	promptCurrent func(*os.File, io.Writer) (string, error)
	ensureOwner   func() error
	// ensureSetupOwner hands the setup token file to the hub's account, so
	// the running hub can read and consume a token issued by root.
	ensureSetupOwner func() error
}

func runLocalAuthCommandWithDependencies(args []string, in *os.File, out io.Writer, dependencies localAuthCommandDependencies) error {
	if len(args) != 2 || args[0] != "local" {
		return errors.New(localAuthUsage)
	}
	command := args[1]
	store := dependencies.newStore()
	if command != "init" && command != "recover" {
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
		printSecondFactor(out, secret, recovery)
		if err := dependencies.ensureOwner(); err != nil {
			return err
		}
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
	case "recover":
		// ADR-0025 total recovery. Running this needs write access to the
		// credential directory, which is the local administrative access the
		// design requires; it takes no argument, reveals no username and
		// never accepts a secret. The token replaces any earlier one.
		token, expiresAt, err := store.IssueSetupToken(hubauth.SetupRecovery, hubauth.DefaultSetupTokenTTL)
		if err != nil {
			return err
		}
		if err := dependencies.ensureSetupOwner(); err != nil {
			return err
		}
		fmt.Fprintf(out, "One-time recovery token (shown once; valid until %s):\n%s\n", expiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"), token)
		fmt.Fprintln(out, "Open /auth/setup on the hub and enter it to replace the username, password, TOTP and recovery codes.")
		fmt.Fprintln(out, "Redeeming it revokes every existing local session.")
	default:
		return errors.New(localAuthUsage)
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

type localAuthOwnerDependencies struct {
	euid        func() int
	lookup      func(string) (*user.User, error)
	chown       func(string, int, int) error
	chmod       func(string, os.FileMode) error
	isContainer func() bool
	warn        func(string, ...any)
}

// Packaging creates the bitacora system user. The official container does not:
// its root-owned volume needs private modes but has no service account to own
// it. Systemd installations still require that account and retain the chown.
func ensureLocalAuthOwner(paths ...string) error {
	return ensureLocalAuthOwnerForPaths(paths, localAuthOwnerDependencies{
		euid:   os.Geteuid,
		lookup: user.Lookup,
		chown:  os.Chown,
		chmod:  os.Chmod,
		isContainer: func() bool {
			_, err := os.Stat("/.dockerenv")
			return err == nil
		},
		warn: log.Printf,
	})
}

func ensureLocalAuthOwnerForPaths(paths []string, dependencies localAuthOwnerDependencies) error {
	if dependencies.euid() != 0 {
		return nil
	}
	account, err := dependencies.lookup("bitacora")
	if err != nil {
		if dependencies.isContainer() {
			for _, path := range paths {
				if err := dependencies.chmod(path, 0o600); err != nil {
					return fmt.Errorf("setting mode for %s: %w", path, err)
				}
			}
			dependencies.warn("bitacora-hub: bitacora user is unavailable in a container; keeping local authentication files root-owned with mode 0600")
			return nil
		}
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
	for _, path := range paths {
		if err := dependencies.chown(path, uid, gid); err != nil {
			return fmt.Errorf("setting owner for %s: %w", path, err)
		}
		if err := dependencies.chmod(path, 0o600); err != nil {
			return fmt.Errorf("setting mode for %s: %w", path, err)
		}
	}
	return nil
}
