package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/transport"
	"github.com/bitacora-dev/bitacora/internal/transport/sqlitetokenstore"
)

// newHubDataDir returns a data directory with an initialized token store,
// which is the state `bita agent` requires: the hub owns tokens.db and this
// command only ever writes into the one the hub already has.
func newHubDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlitetokenstore.New(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("creating token store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing token store: %v", err)
	}
	return dir
}

func runAgentCommand(t *testing.T, dataDir string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	err := runAgent(append(args, "-data-dir", dataDir), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func openDataDirStore(t *testing.T, dataDir string) *sqlitetokenstore.Store {
	t.Helper()
	store, err := sqlitetokenstore.New(filepath.Join(dataDir, "tokens.db"))
	if err != nil {
		t.Fatalf("opening token store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAgentCreateStoresACredentialAndShowsTheTokenOnce(t *testing.T) {
	dataDir := newHubDataDir(t)

	stdout, _, err := runAgentCommand(t, dataDir, "create", "vps-berlin")
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if !strings.Contains(stdout, "shown once and never again") {
		t.Fatalf("output does not make the one-time display explicit:\n%s", stdout)
	}

	token := extractToken(t, stdout)
	store := openDataDirStore(t, dataDir)
	hostID, ok, err := store.Lookup(context.Background(), token)
	if err != nil || !ok {
		t.Fatalf("the printed token does not authenticate: ok=%v err=%v", ok, err)
	}

	tokens, err := store.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Label != "vps-berlin" || tokens[0].HostID != hostID {
		t.Fatalf("tokens = %+v, want one named vps-berlin bound to %s", tokens, hostID)
	}
}

func TestAgentCreateAcceptsAnExistingHostID(t *testing.T) {
	dataDir := newHubDataDir(t)

	if _, _, err := runAgentCommand(t, dataDir, "create", "vps", "-host-id", "01JEXISTING"); err != nil {
		t.Fatalf("creating: %v", err)
	}
	tokens, err := openDataDirStore(t, dataDir).ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tokens) != 1 || tokens[0].HostID != "01JEXISTING" {
		t.Fatalf("tokens = %+v, want the credential bound to 01JEXISTING", tokens)
	}
}

// Two agents with the same name would make revoke and rotate ambiguous, and
// "rotate the one you meant" is not something a CLI can guess.
func TestAgentCreateRefusesADuplicateName(t *testing.T) {
	dataDir := newHubDataDir(t)
	if _, _, err := runAgentCommand(t, dataDir, "create", "vps"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, _, err := runAgentCommand(t, dataDir, "create", "vps")
	if err == nil {
		t.Fatal("a second agent with the same name was accepted")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want it to name the collision", err)
	}
}

func TestAgentCreateRequiresAName(t *testing.T) {
	dataDir := newHubDataDir(t)
	if _, _, err := runAgentCommand(t, dataDir, "create"); err == nil {
		t.Fatal("a nameless agent was created")
	}
}

func TestAgentListShowsEveryCredentialAndItsStatus(t *testing.T) {
	dataDir := newHubDataDir(t)
	for _, name := range []string{"alpha", "beta"} {
		if _, _, err := runAgentCommand(t, dataDir, "create", name); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
	if _, _, err := runAgentCommand(t, dataDir, "revoke", "beta"); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	stdout, _, err := runAgentCommand(t, dataDir, "list")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, want := range []string{"NAME", "alpha", "active", "beta", "revoked", "never"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("listing is missing %q:\n%s", want, stdout)
		}
	}
}

// A listing must not be able to print token material, so there is none in
// what it reads.
func TestAgentListNeverPrintsTheToken(t *testing.T) {
	dataDir := newHubDataDir(t)
	stdout, _, err := runAgentCommand(t, dataDir, "create", "vps")
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	token := extractToken(t, stdout)

	listing, _, err := runAgentCommand(t, dataDir, "list")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if strings.Contains(listing, token) {
		t.Fatalf("the listing printed the plaintext token:\n%s", listing)
	}
	if strings.Contains(listing, "$argon2id$") {
		t.Fatalf("the listing printed a stored hash:\n%s", listing)
	}
}

func TestAgentListReportsAnEmptyStore(t *testing.T) {
	stdout, _, err := runAgentCommand(t, newHubDataDir(t), "list")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if !strings.Contains(stdout, "no agents enrolled") {
		t.Fatalf("output = %q, want it to say the store is empty", stdout)
	}
}

func TestAgentRevokeStopsTheCredential(t *testing.T) {
	dataDir := newHubDataDir(t)
	stdout, _, err := runAgentCommand(t, dataDir, "create", "vps")
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	token := extractToken(t, stdout)

	if _, _, err := runAgentCommand(t, dataDir, "revoke", "vps"); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if _, ok, err := openDataDirStore(t, dataDir).Lookup(context.Background(), token); err != nil || ok {
		t.Fatalf("a revoked token still authenticates: ok=%v err=%v", ok, err)
	}
}

func TestAgentRevokeOfAnUnknownNameFailsLoudly(t *testing.T) {
	_, _, err := runAgentCommand(t, newHubDataDir(t), "revoke", "nothing-here")
	if err == nil {
		t.Fatal("revoking an unknown agent reported success")
	}
	if !strings.Contains(err.Error(), "no agent named") {
		t.Fatalf("error = %v, want it to say the name is unknown", err)
	}
}

func TestAgentRotateKeepsTheOldTokenForTheGraceWindow(t *testing.T) {
	dataDir := newHubDataDir(t)
	created, _, err := runAgentCommand(t, dataDir, "create", "vps")
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	oldToken := extractToken(t, created)

	rotated, _, err := runAgentCommand(t, dataDir, "rotate", "vps")
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}
	newToken := extractToken(t, rotated)
	if newToken == oldToken {
		t.Fatal("rotation returned the same token")
	}
	if !strings.Contains(rotated, "stops working at") {
		t.Fatalf("rotation output does not say when the old credential dies:\n%s", rotated)
	}

	store := openDataDirStore(t, dataDir)
	for _, token := range []string{oldToken, newToken} {
		if _, ok, err := store.Lookup(context.Background(), token); err != nil || !ok {
			t.Fatalf("token should work inside the grace window: ok=%v err=%v", ok, err)
		}
	}
}

func TestAgentRotateWithoutGraceRevokesImmediately(t *testing.T) {
	dataDir := newHubDataDir(t)
	created, _, err := runAgentCommand(t, dataDir, "create", "vps")
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	oldToken := extractToken(t, created)

	rotated, _, err := runAgentCommand(t, dataDir, "rotate", "vps", "-grace", "0")
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}
	if !strings.Contains(rotated, "revoked immediately") {
		t.Fatalf("output does not report the immediate cutover:\n%s", rotated)
	}

	store := openDataDirStore(t, dataDir)
	if _, ok, _ := store.Lookup(context.Background(), oldToken); ok {
		t.Fatal("the superseded token still authenticates")
	}
	if _, ok, err := store.Lookup(context.Background(), extractToken(t, rotated)); err != nil || !ok {
		t.Fatalf("the replacement token does not authenticate: ok=%v err=%v", ok, err)
	}
}

// A rotation preserves the name, because the operator refers to the agent by
// it and a rotation is not a rename.
func TestAgentRotateKeepsTheName(t *testing.T) {
	dataDir := newHubDataDir(t)
	if _, _, err := runAgentCommand(t, dataDir, "create", "vps"); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if _, _, err := runAgentCommand(t, dataDir, "rotate", "vps"); err != nil {
		t.Fatalf("rotating: %v", err)
	}
	tokens, err := openDataDirStore(t, dataDir).ListTokens(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, token := range tokens {
		if token.Label != "vps" {
			t.Fatalf("token %d label = %q, want vps on both the old and the new credential", token.ID, token.Label)
		}
	}
}

// A typo in -data-dir must not create an empty store and report that a hub
// full of agents has none.
func TestAgentCommandsRefuseADataDirWithNoTokenStore(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-the-hub")
	for _, args := range [][]string{{"list"}, {"create", "vps"}, {"revoke", "vps"}, {"rotate", "vps"}} {
		_, _, err := runAgentCommand(t, missing, args...)
		if err == nil {
			t.Fatalf("%v: succeeded against a data dir with no token store", args)
		}
		if !strings.Contains(err.Error(), "no token store at") {
			t.Fatalf("%v: error = %v, want it to name the missing store", args, err)
		}
	}
}

func TestAgentRejectsAnUnknownSubcommand(t *testing.T) {
	_, stderr, err := runAgentCommand(t, newHubDataDir(t), "destroy")
	if err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
	if !strings.Contains(stderr, "usage: bita agent") {
		t.Fatalf("stderr = %q, want the usage text", stderr)
	}
}

// The listing reports the enrollment codes that were issued and never
// redeemed: an enrollment nobody finished is the one state no credential row
// records, and the operator who is waiting for a server to appear needs it.
func TestAgentListReportsPendingEnrollmentCodes(t *testing.T) {
	dataDir := newHubDataDir(t)
	store := openDataDirStore(t, dataDir)
	if err := store.CreateEnrollmentCode(context.Background(), "pending-code", transport.EnrollmentCode{
		Label: "vps", ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatalf("creating code: %v", err)
	}

	stdout, _, err := runAgentCommand(t, dataDir, "list")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if !strings.Contains(stdout, "not yet redeemed") {
		t.Fatalf("listing does not mention the pending enrollment:\n%s", stdout)
	}
}

// extractToken pulls the one-time token out of the command's own output, so
// the test reads it exactly where an operator would.
func extractToken(t *testing.T, output string) string {
	t.Helper()
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "shown once") {
			continue
		}
		if i+1 >= len(lines) {
			break
		}
		token := strings.TrimSpace(lines[i+1])
		if token == "" {
			break
		}
		return token
	}
	t.Fatalf("no one-time token found in output:\n%s", output)
	return ""
}
