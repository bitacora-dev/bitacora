package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DefaultTokenPath is where ADR-0008 fixes the agent's ingest token:
// /etc/bitacora/token, mode 0600, owner root:bitacora.
const DefaultTokenPath = "/etc/bitacora/token"

// enrollTimeout bounds the one request enrollment makes. It is generous
// because the hub verifies the code with Argon2id, once per live code.
const enrollTimeout = 30 * time.Second

// enrollUsage is printed when the command is invoked wrongly.
const enrollUsage = `usage: bitacora-agent enroll -hub URL [-code CODE | -code -] [flags]

  -hub URL            hub base URL (also BITACORA_HUB_URL)
  -code CODE           one-time enrollment code; "-" reads it from stdin
                       (also BITACORA_ENROLL_CODE)
  -token-file PATH     where to write the ingest token (default ` + DefaultTokenPath + `)
  -host-id-path PATH   where the persistent host_id lives (default ` + schema.DefaultHostIDPath + `)
  -force               overwrite an existing token file

Prefer "-code -" and pipe the code in: a code passed as an argument ends up
in the shell history and in the process list of every user on the machine.

This is an installer step, run once by the operator. The agent daemon never
runs it and never writes outside /var/lib/bitacora (ADR-0005).
`

// enrollResponse mirrors hubapi.EnrollResponse. It is declared here rather
// than imported because the agent must not depend on a hub-side package:
// this is the wire contract of POST /v1/enroll, which is exactly the kind of
// thing internal/transport exists to own — but the response is JSON on the
// read-side API, not the Protobuf ingest protocol, so it lives with its one
// consumer.
type enrollResponse struct {
	HostID     string `json:"host_id"`
	Token      string `json:"token"`
	HostIDPath string `json:"host_id_path"`
	TokenPath  string `json:"token_path"`
}

type enrollConfig struct {
	hubURL      string
	code        string
	tokenFile   string
	hostIDPath  string
	force       bool
	existingSet bool
}

// runEnroll implements `bitacora-agent enroll`: it exchanges a one-time code
// for this machine's host_id and ingest token, and writes both where the
// daemon expects them (ADR-0008).
//
// It is a separate subcommand rather than a startup mode on purpose. The
// daemon runs unprivileged and may not write /etc/bitacora (ADR-0005), so
// enrolling is an operator action performed once, before the service starts
// — not something a long-running process does to itself.
func runEnroll(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cfg, err := parseEnrollConfig(args, stdin, stderr)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{"code": cfg.code, "host_id": existingHostID(cfg.hostIDPath)})
	if err != nil {
		return fmt.Errorf("encoding enrollment request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), enrollTimeout)
	defer cancel()
	enrolled, err := requestEnrollment(ctx, cfg.hubURL, body)
	if err != nil {
		return err
	}

	if err := writeHostID(cfg.hostIDPath, enrolled.HostID, cfg.force); err != nil {
		return err
	}
	if err := writeTokenFile(cfg.tokenFile, enrolled.Token, cfg.force); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "enrolled as %s\n", enrolled.HostID)
	fmt.Fprintf(stdout, "  host_id: %s\n", cfg.hostIDPath)
	fmt.Fprintf(stdout, "  token:   %s (mode 0600)\n", cfg.tokenFile)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "The token is not printed: it is already on disk, and the hub keeps only its")
	fmt.Fprintln(stdout, "Argon2id hash. Set the owner the unit expects (chown root:bitacora) and start")
	fmt.Fprintln(stdout, "bitacora-agent.")
	return nil
}

func parseEnrollConfig(args []string, stdin io.Reader, stderr io.Writer) (enrollConfig, error) {
	flags := flag.NewFlagSet("bitacora-agent enroll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var cfg enrollConfig
	flags.StringVar(&cfg.hubURL, "hub", os.Getenv("BITACORA_HUB_URL"), "hub base URL")
	flags.StringVar(&cfg.code, "code", os.Getenv("BITACORA_ENROLL_CODE"), `one-time enrollment code; "-" reads it from stdin`)
	flags.StringVar(&cfg.tokenFile, "token-file", DefaultTokenPath, "where to write the ingest token")
	flags.StringVar(&cfg.hostIDPath, "host-id-path", schema.DefaultHostIDPath, "where the persistent host_id lives")
	flags.BoolVar(&cfg.force, "force", false, "overwrite an existing token file")
	if err := flags.Parse(args); err != nil {
		return enrollConfig{}, err
	}

	if cfg.hubURL == "" {
		fmt.Fprint(stderr, enrollUsage)
		return enrollConfig{}, errors.New("enroll: -hub is required")
	}
	// An empty -code means stdin as well: the code is a secret, and reading
	// it from a pipe is the form that keeps it out of the shell history and
	// out of /proc/<pid>/cmdline, which every user on the machine can read.
	if cfg.code == "" || cfg.code == "-" {
		code, err := readEnrollCode(stdin)
		if err != nil {
			return enrollConfig{}, err
		}
		cfg.code = code
	}
	if cfg.code == "" {
		fmt.Fprint(stderr, enrollUsage)
		return enrollConfig{}, errors.New("enroll: an enrollment code is required")
	}
	return cfg, nil
}

func readEnrollCode(stdin io.Reader) (string, error) {
	// Bounded: this reads a ~27-character code, and an unbounded read on a
	// pipe nobody closes would hang the installer.
	data, err := io.ReadAll(io.LimitReader(stdin, 4096))
	if err != nil {
		return "", fmt.Errorf("reading the enrollment code from stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// existingHostID returns the host_id this machine already has, or "" when it
// has none. Sending it lets the hub re-enroll an agent without handing it a
// second identity, which would orphan its history (ADR-0004).
func existingHostID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func requestEnrollment(ctx context.Context, hubURL string, body []byte) (enrollResponse, error) {
	endpoint := strings.TrimSuffix(hubURL, "/") + "/v1/enroll"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return enrollResponse{}, fmt.Errorf("building enrollment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return enrollResponse{}, fmt.Errorf("calling %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		// The hub answers one generic rejection for every bad code, so
		// there is nothing more specific to relay. Reading a bounded
		// prefix keeps a misconfigured URL that serves an HTML page from
		// pasting a whole document into the terminal.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return enrollResponse{}, fmt.Errorf("enrollment refused by %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(detail)))
	}

	var enrolled enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&enrolled); err != nil {
		return enrollResponse{}, fmt.Errorf("decoding enrollment response: %w", err)
	}
	if enrolled.HostID == "" || enrolled.Token == "" {
		return enrollResponse{}, errors.New("the hub accepted the code but returned no host_id or token")
	}
	return enrolled, nil
}

// writeTokenFile persists the ingest token at 0600, refusing to overwrite an
// existing one unless the operator asked for it. Overwriting silently would
// disconnect a working agent on a mistyped command, and the token it
// replaced cannot be recovered — the hub stores only its hash.
func writeTokenFile(path, token string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	// O_EXCL does the refusing, so there is no window between checking for
	// the file and creating it. 0600 is passed to open rather than chmodded
	// afterwards: the token must never exist on disk at a wider mode, not
	// even for the microsecond in between.
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; pass -force to replace the current token (the old one cannot be recovered)", path)
		}
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	if _, err := file.WriteString(token + "\n"); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("fsyncing %s: %w", path, err)
	}
	// Re-asserted because -force reuses an existing file, which keeps
	// whatever mode it already had.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restricting %s to 0600: %w", path, err)
	}
	return nil
}

// writeHostID persists the identity the hub assigned. An existing host_id
// that already matches is left alone; one that differs is a refusal rather
// than an overwrite, because replacing it detaches the machine from its own
// history (ADR-0004).
func writeHostID(path, hostID string, force bool) error {
	switch existing := existingHostID(path); {
	case existing == "":
	case existing == hostID:
		return nil
	case !force:
		return fmt.Errorf("%s already holds host_id %s, and the hub enrolled this machine as %s; pass -force to replace it, which detaches this machine from its existing history", path, existing, hostID)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(hostID+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
