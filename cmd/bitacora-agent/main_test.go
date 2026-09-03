package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

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
	regs, disabled := reg.Resolve(context.Background(), collector.Config{}, &collector.HostInfo{}, map[collector.Capability]bool{})

	names := map[string]bool{}
	for _, reg := range regs {
		names[reg.Collector.Name()] = true
	}
	for _, disabled := range disabled {
		names[disabled.Name] = true
	}

	for _, name := range []string{"cpu", "memory", "docker", "journald"} {
		if !names[name] {
			t.Fatalf("expected production collector %q to be assembled in bitacora-agent registry; got %v", name, names)
		}
	}
	if names["example"] {
		t.Fatal("example collector must not be assembled in the production agent registry by default")
	}
}
