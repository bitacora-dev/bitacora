package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/internal/transport/sqlitetokenstore"
	"github.com/oklog/ulid/v2"
)

// DefaultDataDir is the hub's data directory (ADR-0003), which is where its
// token store lives. `bita agent` writes the very same tokens.db the running
// hub reads, rather than a parallel registry of its own.
const DefaultDataDir = "/var/lib/bitacora"

// ingestTokenBytes is the entropy behind a generated ingest token, matching
// what the hub's own POST /v1/hosts mints.
const ingestTokenBytes = 32

// DefaultRotationGrace mirrors hubapi.DefaultRotationGrace: the rotated-out
// credential keeps working for this long, so the agent stays connected while
// the operator installs the replacement.
const DefaultRotationGrace = 15 * time.Minute

// agentUsage is printed for a missing or unknown `bita agent` subcommand.
const agentUsage = `usage: bita agent create <name> [-host-id ID]
       bita agent list
       bita agent revoke <name>
       bita agent rotate <name> [-grace DURATION]

Common flags: -data-dir (default ` + DefaultDataDir + `)

The token store is owned by the unprivileged hub user (ADR-0005), so run
these as that user, for example:

    sudo -u bitacora bita agent list
`

// runAgent implements `bita agent <subcommand>`: the operator-side lifecycle
// of ADR-0008 ingest credentials — create, list, revoke, rotate — against
// the hub's own token store.
//
// It is a local administrative command, not a client of the hub's API. That
// is deliberate: it has to keep working when the hub is down or before any
// device has been paired, which is the situation in which an operator most
// needs to look at or replace a credential. ADR-0012 is untouched — nothing
// here executes anything, it opens a SQLite file.
func runAgent(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentUsage)
		return errors.New("bita agent: a subcommand is required")
	}

	switch args[0] {
	case "create":
		return runAgentCreate(args[1:], stdout)
	case "list":
		return runAgentList(args[1:], stdout)
	case "revoke":
		return runAgentRevoke(args[1:], stdout)
	case "rotate":
		return runAgentRotate(args[1:], stdout)
	default:
		fmt.Fprint(stderr, agentUsage)
		return fmt.Errorf("bita agent: unknown subcommand %q", args[0])
	}
}

// agentFlags is the flag set every subcommand shares.
func agentFlags(name string) (*flag.FlagSet, *string) {
	flags := flag.NewFlagSet("bita agent "+name, flag.ContinueOnError)
	dataDir := flags.String("data-dir", DefaultDataDir, "hub data directory holding tokens.db")
	return flags, dataDir
}

// parseAgentArgs parses flags that may appear on either side of the agent
// name. Go's flag package stops at the first non-flag argument, so
// `bita agent create vps -data-dir /srv/bitacora` would otherwise parse the
// name and silently ignore the flag — reading or writing the wrong token
// store without saying so. The name is hoisted out first and the rest is
// handed to the flag set, so both orders behave the same.
//
// valueFlags are the flags that take a separate value argument, which must
// not be mistaken for the positional name.
func parseAgentArgs(flags *flag.FlagSet, args []string, valueFlags map[string]bool) (string, error) {
	var (
		name      string
		remaining []string
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			remaining = append(remaining, arg)
			flagName := strings.TrimLeft(arg, "-")
			if !strings.Contains(arg, "=") && valueFlags[flagName] && i+1 < len(args) {
				i++
				remaining = append(remaining, args[i])
			}
			continue
		}
		if name == "" {
			name = arg
			continue
		}
		return "", fmt.Errorf("unexpected extra argument %q", arg)
	}
	if err := flags.Parse(remaining); err != nil {
		return "", err
	}
	return name, nil
}

// agentValueFlags are every flag in this command that takes a value.
var agentValueFlags = map[string]bool{"data-dir": true, "host-id": true, "grace": true}

func runAgentCreate(args []string, stdout io.Writer) error {
	flags, dataDir := agentFlags("create")
	hostID := flags.String("host-id", "", "enroll with this existing host_id instead of generating one")
	name, err := parseAgentArgs(flags, args, agentValueFlags)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("bita agent create: a name is required")
	}

	store, openErr := openTokenStore(*dataDir)
	if openErr != nil {
		return openErr
	}
	defer store.Close()

	ctx := context.Background()
	if err := refuseDuplicateName(ctx, store, name); err != nil {
		return err
	}

	id := *hostID
	if id == "" {
		id = ulid.Make().String()
	}
	token, err := newIngestToken()
	if err != nil {
		return err
	}
	if _, err := store.AddLabeledToken(ctx, id, name, token); err != nil {
		return fmt.Errorf("storing credential for %q: %w", name, err)
	}

	fmt.Fprintf(stdout, "agent %q created\n", name)
	fmt.Fprintf(stdout, "  host_id: %s\n", id)
	printTokenOnce(stdout, token)
	return nil
}

func runAgentList(args []string, stdout io.Writer) error {
	flags, dataDir := agentFlags("list")
	if _, err := parseAgentArgs(flags, args, agentValueFlags); err != nil {
		return err
	}

	store, err := openTokenStore(*dataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		return fmt.Errorf("listing credentials: %w", err)
	}
	pending, err := store.PendingEnrollmentCodes(ctx)
	if err != nil {
		return err
	}

	fmt.Fprint(stdout, formatAgentList(tokens, pending, time.Now().UTC()))
	return nil
}

func runAgentRevoke(args []string, stdout io.Writer) error {
	flags, dataDir := agentFlags("revoke")
	name, err := parseAgentArgs(flags, args, agentValueFlags)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("bita agent revoke: a name is required")
	}

	store, openErr := openTokenStore(*dataDir)
	if openErr != nil {
		return openErr
	}
	defer store.Close()

	ctx := context.Background()
	hostID, err := resolveAgentName(ctx, store, name)
	if err != nil {
		return err
	}
	revoked, err := store.RevokeHostTokens(ctx, hostID)
	if err != nil {
		return fmt.Errorf("revoking %q: %w", name, err)
	}
	if revoked == 0 {
		fmt.Fprintf(stdout, "agent %q (host %s) was already revoked; nothing changed\n", name, hostID)
		return nil
	}
	fmt.Fprintf(stdout, "agent %q (host %s): %d credential(s) revoked\n", name, hostID, revoked)
	fmt.Fprintln(stdout, "Its next ingest request is refused. The agent keeps buffering locally (ADR-0008),")
	fmt.Fprintln(stdout, "so rotating it later recovers the backlog rather than losing it.")
	return nil
}

func runAgentRotate(args []string, stdout io.Writer) error {
	flags, dataDir := agentFlags("rotate")
	grace := flags.Duration("grace", DefaultRotationGrace, "how long the superseded credential keeps working; 0 revokes it immediately")
	name, err := parseAgentArgs(flags, args, agentValueFlags)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("bita agent rotate: a name is required")
	}
	if *grace < 0 {
		return errors.New("bita agent rotate: -grace cannot be negative")
	}

	store, openErr := openTokenStore(*dataDir)
	if openErr != nil {
		return openErr
	}
	defer store.Close()

	ctx := context.Background()
	hostID, err := resolveAgentName(ctx, store, name)
	if err != nil {
		return err
	}
	token, err := newIngestToken()
	if err != nil {
		return err
	}
	rotated, err := store.RotateHostTokens(ctx, hostID, name, token, *grace)
	if err != nil {
		return fmt.Errorf("rotating %q: %w", name, err)
	}

	fmt.Fprintf(stdout, "agent %q (host %s) rotated\n", name, hostID)
	if *grace > 0 {
		fmt.Fprintf(stdout, "  the previous credential stops working at %s (in %s)\n",
			rotated.CreatedAt.Add(*grace).Format(time.RFC3339), *grace)
	} else {
		fmt.Fprintln(stdout, "  the previous credential was revoked immediately")
	}
	printTokenOnce(stdout, token)
	return nil
}

// printTokenOnce is the only place a plaintext ingest token is ever printed.
// It says so out loud, because the hub stores nothing but an Argon2id hash
// and no command can show this value a second time.
func printTokenOnce(stdout io.Writer, token string) {
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "  === token, shown once and never again ===")
	fmt.Fprintf(stdout, "  %s\n", token)
	fmt.Fprintln(stdout, "  =========================================")
	fmt.Fprintln(stdout, "")
	fmt.Fprintf(stdout, "  Install it on the agent at %s, mode 0600, owner root:bitacora.\n", agentTokenPath)
	fmt.Fprintln(stdout, "  The hub keeps only its Argon2id hash: if you lose it, rotate instead of looking for it.")
}

// agentTokenPath is where ADR-0008 fixes the agent's token file.
const agentTokenPath = "/etc/bitacora/token"

func formatAgentList(tokens []transport.TokenInfo, pendingCodes int, now time.Time) string {
	if len(tokens) == 0 && pendingCodes == 0 {
		return "no agents enrolled\n"
	}

	// Newest credential per host: a rotated host has several rows, and the
	// one that describes it is the last one issued.
	newest := make(map[string]transport.TokenInfo, len(tokens))
	for _, token := range tokens {
		if current, ok := newest[token.HostID]; ok && current.ID > token.ID {
			continue
		}
		newest[token.HostID] = token
	}
	current := make([]transport.TokenInfo, 0, len(newest))
	for _, token := range newest {
		current = append(current, token)
	}
	sort.Slice(current, func(i, j int) bool { return current[i].ID < current[j].ID })

	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tHOST_ID\tSTATUS\tCREATED\tLAST SEEN")
	for _, token := range current {
		name := token.Label
		if name == "" {
			// A credential from `bitacora-hub -add-token` or from a
			// pre-lifecycle tokens.db has no name. Saying so beats printing
			// an empty column an operator has to interpret.
			name = "(unnamed)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			name, token.HostID, agentStatus(token, now),
			token.CreatedAt.Format(time.RFC3339), formatLastSeen(token.LastSeenAt))
	}
	_ = w.Flush()

	if pendingCodes > 0 {
		fmt.Fprintf(buf, "\n%d enrollment code(s) issued and not yet redeemed\n", pendingCodes)
	}
	return buf.String()
}

func agentStatus(token transport.TokenInfo, now time.Time) string {
	switch {
	case token.Revoked():
		return "revoked"
	case token.Expired(now):
		return "expired"
	case !token.ExpiresAt.IsZero():
		return "rotating"
	default:
		return "active"
	}
}

func formatLastSeen(at time.Time) string {
	if at.IsZero() {
		return "never"
	}
	return at.Format(time.RFC3339)
}

// resolveAgentName maps an operator-facing name to the host_id its
// credential is bound to. A name that matches more than one host is an error
// rather than a guess: revoking the wrong server's credential silently is
// worse than refusing to act.
func resolveAgentName(ctx context.Context, store *sqlitetokenstore.Store, name string) (string, error) {
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		return "", fmt.Errorf("listing credentials: %w", err)
	}

	matches := map[string]struct{}{}
	for _, token := range tokens {
		if token.Label == name || token.HostID == name {
			matches[token.HostID] = struct{}{}
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no agent named %q (try `bita agent list`)", name)
	case 1:
		for hostID := range matches {
			return hostID, nil
		}
		return "", nil
	default:
		hosts := make([]string, 0, len(matches))
		for hostID := range matches {
			hosts = append(hosts, hostID)
		}
		sort.Strings(hosts)
		return "", fmt.Errorf("%q matches more than one host (%v); name the host_id instead", name, hosts)
	}
}

// refuseDuplicateName keeps `bita agent list` unambiguous, and keeps revoke
// and rotate able to resolve a name at all.
func refuseDuplicateName(ctx context.Context, store *sqlitetokenstore.Store, name string) error {
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		return fmt.Errorf("listing credentials: %w", err)
	}
	for _, token := range tokens {
		if token.Label == name {
			return fmt.Errorf("an agent named %q already exists (host %s); rotate it instead of creating a second credential", name, token.HostID)
		}
	}
	return nil
}

// openTokenStore opens the hub's token store, refusing to create a new one
// where none exists. A typo in -data-dir must not silently produce an empty
// registry that reports "no agents enrolled" about a hub full of them.
func openTokenStore(dataDir string) (*sqlitetokenstore.Store, error) {
	path := filepath.Join(dataDir, "tokens.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no token store at %s: is -data-dir right, and has the hub run yet?", path)
		}
		if errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("cannot read %s: %w (ADR-0005: the store belongs to the hub's own user, so run `sudo -u bitacora bita agent ...`)", path, err)
		}
		return nil, fmt.Errorf("checking %s: %w", path, err)
	}
	store, err := sqlitetokenstore.New(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (ADR-0005: run this as the hub's own user, e.g. `sudo -u bitacora bita agent ...`)", path, err)
	}
	return store, nil
}

func newIngestToken() (string, error) {
	buf := make([]byte, ingestTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating ingest token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
