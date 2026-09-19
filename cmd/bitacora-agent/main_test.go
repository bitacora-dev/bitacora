package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/packageexecutor"
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
	want := []string{"cpu", "diskarray", "docker", "hwidentity", "journald", "memory", "network", "operations", "package-actions", "pkgupdates", "public_surface", "shares", "shareusage", "ups", "users"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected production collector catalog: got %v, want %v", got, want)
	}
}
