package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// enrollHub is a stand-in for POST /v1/enroll. It records what the agent
// sent, which is how the test asserts the agent offers its existing
// host_id rather than silently accepting a new identity.
type enrollHub struct {
	status     int
	hostID     string
	token      string
	gotCode    string
	gotHostID  string
	calls      int
	bodyIsJSON bool
}

func (h *enrollHub) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls++
		var body struct {
			Code   string `json:"code"`
			HostID string `json:"host_id"`
		}
		h.bodyIsJSON = json.NewDecoder(r.Body).Decode(&body) == nil
		h.gotCode, h.gotHostID = body.Code, body.HostID

		status := h.status
		if status == 0 {
			status = http.StatusCreated
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusCreated {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired enrollment code"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"host_id":      h.hostID,
			"token":        h.token,
			"host_id_path": "/var/lib/bitacora/host_id",
			"token_path":   "/etc/bitacora/token",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func enrollPaths(t *testing.T) (tokenFile, hostIDPath string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "etc", "token"), filepath.Join(dir, "lib", "host_id")
}

func TestEnrollWritesTheTokenAt0600AndThePersistedHostID(t *testing.T) {
	hub := &enrollHub{hostID: "01JENROLLED", token: "secret-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)

	var stdout, stderr strings.Builder
	err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("enrolling: %v (stderr %q)", err, stderr.String())
	}

	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "secret-token" {
		t.Fatalf("token file holds %q, want the issued token", strings.TrimSpace(string(data)))
	}
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("stating token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %04o, want 0600 (ADR-0008)", perm)
	}

	persisted, err := os.ReadFile(hostIDPath)
	if err != nil {
		t.Fatalf("reading host_id: %v", err)
	}
	if strings.TrimSpace(string(persisted)) != "01JENROLLED" {
		t.Fatalf("host_id = %q, want the one the hub assigned", strings.TrimSpace(string(persisted)))
	}
	if hub.gotCode != "the-code" {
		t.Fatalf("hub received code %q, want the-code", hub.gotCode)
	}
}

// The token is on disk and the hub keeps only its hash, so printing it would
// put it in the terminal scrollback for nothing.
func TestEnrollDoesNotPrintTheToken(t *testing.T) {
	hub := &enrollHub{hostID: "01JENROLLED", token: "secret-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)

	var stdout, stderr strings.Builder
	if err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("enrolling: %v", err)
	}
	if strings.Contains(stdout.String(), "secret-token") {
		t.Fatalf("the token was printed:\n%s", stdout.String())
	}
}

// Reading the code from stdin is the form that keeps it out of the shell
// history and out of the process list.
func TestEnrollReadsTheCodeFromStdin(t *testing.T) {
	hub := &enrollHub{hostID: "01JENROLLED", token: "secret-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)

	for _, args := range [][]string{
		{"-hub", url, "-code", "-", "-token-file", tokenFile, "-host-id-path", hostIDPath},
		{"-hub", url, "-token-file", tokenFile, "-host-id-path", hostIDPath},
	} {
		if err := os.RemoveAll(filepath.Dir(tokenFile)); err != nil {
			t.Fatalf("clearing token dir: %v", err)
		}
		var stdout, stderr strings.Builder
		if err := runEnroll(args, strings.NewReader("piped-code\n"), &stdout, &stderr); err != nil {
			t.Fatalf("%v: enrolling: %v (stderr %q)", args, err, stderr.String())
		}
		if hub.gotCode != "piped-code" {
			t.Fatalf("%v: hub received code %q, want the piped one with its newline trimmed", args, hub.gotCode)
		}
	}
}

// An agent that already has an identity must offer it, so the hub can
// re-enroll it instead of orphaning its history (ADR-0004).
func TestEnrollOffersTheExistingHostID(t *testing.T) {
	hub := &enrollHub{hostID: "01JEXISTING", token: "secret-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)
	if err := os.MkdirAll(filepath.Dir(hostIDPath), 0o755); err != nil {
		t.Fatalf("creating host_id dir: %v", err)
	}
	if err := os.WriteFile(hostIDPath, []byte("01JEXISTING\n"), 0o644); err != nil {
		t.Fatalf("seeding host_id: %v", err)
	}

	var stdout, stderr strings.Builder
	if err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("enrolling: %v", err)
	}
	if hub.gotHostID != "01JEXISTING" {
		t.Fatalf("hub received host_id %q, want the agent's existing one", hub.gotHostID)
	}
}

// Overwriting a working agent's token on a mistyped command would
// disconnect it, and the replaced token cannot be recovered.
func TestEnrollRefusesToOverwriteAnExistingTokenFile(t *testing.T) {
	hub := &enrollHub{hostID: "01JENROLLED", token: "new-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o755); err != nil {
		t.Fatalf("creating token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte("current-token\n"), 0o600); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	var stdout, stderr strings.Builder
	err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("an existing token file was overwritten without -force")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Fatalf("error = %v, want it to name the way out", err)
	}
	data, readErr := os.ReadFile(tokenFile)
	if readErr != nil {
		t.Fatalf("reading token file: %v", readErr)
	}
	if strings.TrimSpace(string(data)) != "current-token" {
		t.Fatalf("token file = %q, want the original left untouched", strings.TrimSpace(string(data)))
	}
}

func TestEnrollOverwritesWithForceAndKeeps0600(t *testing.T) {
	hub := &enrollHub{hostID: "01JENROLLED", token: "new-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o755); err != nil {
		t.Fatalf("creating token dir: %v", err)
	}
	// Deliberately wider than 0600, which is what -force has to correct.
	if err := os.WriteFile(tokenFile, []byte("current-token\n"), 0o644); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	var stdout, stderr strings.Builder
	if err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath, "-force"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("enrolling with -force: %v", err)
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "new-token" {
		t.Fatalf("token file = %q, want the replacement", strings.TrimSpace(string(data)))
	}
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("stating token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %04o after -force, want 0600", perm)
	}
}

// Replacing a host_id detaches the machine from its own history, so it is a
// refusal rather than a silent overwrite — and the token must not be written
// either, or the agent would be left with a credential for another identity.
func TestEnrollRefusesAHostIDThatContradictsTheStoredOne(t *testing.T) {
	hub := &enrollHub{hostID: "01JDIFFERENT", token: "new-token"}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)
	if err := os.MkdirAll(filepath.Dir(hostIDPath), 0o755); err != nil {
		t.Fatalf("creating host_id dir: %v", err)
	}
	if err := os.WriteFile(hostIDPath, []byte("01JEXISTING\n"), 0o644); err != nil {
		t.Fatalf("seeding host_id: %v", err)
	}

	var stdout, stderr strings.Builder
	err := runEnroll([]string{"-hub", url, "-code", "the-code", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("the stored host_id was replaced without -force")
	}
	if _, statErr := os.Stat(tokenFile); statErr == nil {
		t.Fatal("the token was written even though the enrollment was refused")
	}
}

func TestEnrollReportsARefusedCodeWithoutWritingAnything(t *testing.T) {
	hub := &enrollHub{status: http.StatusUnauthorized}
	url := hub.start(t)
	tokenFile, hostIDPath := enrollPaths(t)

	var stdout, stderr strings.Builder
	err := runEnroll([]string{"-hub", url, "-code", "wrong", "-token-file", tokenFile, "-host-id-path", hostIDPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("a refused enrollment reported success")
	}
	if _, statErr := os.Stat(tokenFile); statErr == nil {
		t.Fatal("a refused enrollment still wrote a token file")
	}
	if _, statErr := os.Stat(hostIDPath); statErr == nil {
		t.Fatal("a refused enrollment still wrote a host_id")
	}
}

func TestEnrollRequiresAHubURL(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := runEnroll([]string{"-code", "the-code"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("enrolling without -hub reported success")
	}
	if !strings.Contains(stderr.String(), "usage: bitacora-agent enroll") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestEnrollRequiresACode(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := runEnroll([]string{"-hub", "http://hub.invalid"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("enrolling with an empty code reported success")
	}
}
