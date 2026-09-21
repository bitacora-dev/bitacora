package main

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/blackbox"
	"github.com/bitacora-dev/bitacora/internal/packageexecutor"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/proto/bitacorapb"
)

func TestPackageActionRequestHasOnlyFixedOperations(t *testing.T) {
	tests := []struct {
		name      string
		operation bitacorapb.PackageOperation
		want      packageexecutor.Operation
		ok        bool
	}{
		{name: "refresh", operation: bitacorapb.PackageOperation_REFRESH_PACKAGE_CACHE, want: packageexecutor.RefreshPackageCache, ok: true},
		{name: "apply", operation: bitacorapb.PackageOperation_APPLY_PENDING_PACKAGE_UPDATES, want: packageexecutor.ApplyPendingPackageUpdates, ok: true},
		{name: "unknown", operation: bitacorapb.PackageOperation_PACKAGE_OPERATION_UNSPECIFIED},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, ok := packageActionRequest(&bitacorapb.PendingPackageOperation{RequestId: "request-1", Operation: test.operation}, "host-a")
			if ok != test.ok || request.Operation != test.want {
				t.Fatalf("request = %+v, ok = %t", request, ok)
			}
		})
	}
}

func TestActionConfigurationDisabledEvent(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "unreadable file", err: errors.New("opening action configuration: permission denied")},
		{name: "malformed JSON", err: errors.New("decoding action configuration: unexpected end of JSON input")},
		{name: "unknown field", err: errors.New("decoding action configuration: json: unknown field \"unexpected\"")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, ok := actionConfigurationDisabledEvent("host-a", "/etc/bitacora/actions.json", test.err, time.Unix(100, 0))
			if !ok {
				t.Fatal("failed action configuration must emit an event")
			}
			if event.Type != "agent.action_configuration_disabled" || event.Severity != schema.SeverityWarn {
				t.Fatalf("unexpected event identity: %+v", event)
			}
			if event.Attrs["path"] != "/etc/bitacora/actions.json" || event.Attrs["reason"] != test.err.Error() {
				t.Fatalf("event did not preserve diagnostic context: %+v", event.Attrs)
			}
		})
	}

	if _, ok := actionConfigurationDisabledEvent("host-a", "/etc/bitacora/actions.json", nil, time.Unix(100, 0)); ok {
		t.Fatal("missing action configuration is normal and must not emit an event")
	}
}

func TestBlackboxFailureEventMakesDegradationVisible(t *testing.T) {
	err := errors.New("permission denied")
	event := blackboxFailureEvent("host-a", "/var/lib/bitacora/blackbox.dat", "start", err, time.Unix(100, 0))

	if event.Type != "agent.blackbox_recorder_degraded" || event.Severity != schema.SeverityWarn {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Attrs["path"] != "/var/lib/bitacora/blackbox.dat" || event.Attrs["stage"] != "start" || event.Attrs["reason"] != err.Error() {
		t.Fatalf("event did not preserve diagnostic context: %+v", event.Attrs)
	}
}

func TestRunBlackboxWritesFileReadableByBitaAndStops(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the bita command against a real 1 Hz blackbox recorder")
	}

	path := filepath.Join(t.TempDir(), "blackbox.dat")
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runBlackbox(stop, path, nil) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		samples, err := blackbox.Dump(path)
		if err == nil && len(samples) > 0 {
			break
		}
		if time.Now().After(deadline) {
			close(stop)
			<-done
			t.Fatalf("blackbox did not record a sample by the deadline: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blackbox runner returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blackbox runner did not stop after its stop channel closed")
	}

	output, err := exec.Command("go", "run", "../bita", "blackbox", "dump", path).CombinedOutput()
	if err != nil {
		t.Fatalf("bita blackbox dump failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "1 sample(s)") {
		t.Fatalf("bita did not read the agent-written blackbox file: %s", output)
	}
}

func TestReadToken_PrefersTokenFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("unexpected error writing token file: %v", err)
	}

	token, err := readToken(tokenFile, "from-env")
	if err != nil {
		t.Fatalf("unexpected error reading token: %v", err)
	}
	if token != "from-file" {
		t.Fatalf("expected token file value, got %q", token)
	}
}

func TestParseConfig_UsesHubURLFlagAndTokenFileWithoutPlainTokenFlag(t *testing.T) {
	t.Setenv("BITACORA_HUB_URL", "")
	t.Setenv("BITACORA_TOKEN", "")
	t.Setenv("BITACORA_TOKEN_FILE", "")

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatalf("unexpected error writing token file: %v", err)
	}

	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	})
	os.Args = []string{"bitacora-agent", "-hub-url=http://127.0.0.1:8081", "-token-file=" + tokenFile}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	cfg, err := parseConfig()
	if err != nil {
		t.Fatalf("unexpected error parsing config: %v", err)
	}
	if cfg.hubURL != "http://127.0.0.1:8081" {
		t.Fatalf("expected hub URL from flag, got %q", cfg.hubURL)
	}
	if cfg.token != "secret-token" {
		t.Fatalf("expected token from file, got %q", cfg.token)
	}
	if flag.Lookup("token") != nil {
		t.Fatal("plain -token flag must not exist")
	}
}

func TestParseConfig_RejectsHubURLWithoutTokenSource(t *testing.T) {
	t.Setenv("BITACORA_HUB_URL", "")
	t.Setenv("BITACORA_TOKEN", "")
	t.Setenv("BITACORA_TOKEN_FILE", "")

	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	})
	os.Args = []string{"bitacora-agent", "-hub-url=http://127.0.0.1:8081"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	if _, err := parseConfig(); err == nil {
		t.Fatal("expected hub URL without token source to be rejected")
	}
}

func TestBuildRegistryIncludesProductionCollectors(t *testing.T) {
	reg := buildRegistry()
	want := []string{"cpu", "diskarray", "docker", "hwidentity", "hwmon", "journald", "memory", "network", "operations", "package-actions", "pkgupdates", "public_surface", "shares", "shareusage", "ups", "users"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected production collector catalog: got %v, want %v", got, want)
	}
}
