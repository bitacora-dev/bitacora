package hubauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalConfigFromEnvDefaultsToTheADRPaths(t *testing.T) {
	t.Setenv(EnvLocalAuth, "")
	t.Setenv(EnvLocalAuthPath, "")
	t.Setenv(EnvLocalAuthKeyPath, "")
	cfg, err := LocalConfigFromEnv()
	if err != nil {
		t.Fatalf("LocalConfigFromEnv: %v", err)
	}
	if cfg.Required {
		t.Error("an unset switch must not require local authentication")
	}
	if cfg.StatePath() != DefaultLocalAuthPath || cfg.KeyStatePath() != DefaultLocalAuthKeyPath {
		t.Errorf("paths = %q, %q; want the ADR-0023 defaults", cfg.StatePath(), cfg.KeyStatePath())
	}
}

func TestLocalConfigFromEnvReadsSwitchAndPaths(t *testing.T) {
	t.Setenv(EnvLocalAuth, "true")
	t.Setenv(EnvLocalAuthPath, "/var/lib/bitacora/local-auth.json")
	t.Setenv(EnvLocalAuthKeyPath, "/var/lib/bitacora/local-auth.key")
	cfg, err := LocalConfigFromEnv()
	if err != nil {
		t.Fatalf("LocalConfigFromEnv: %v", err)
	}
	if !cfg.Required {
		t.Error("BITACORA_LOCAL_AUTH=true must require local authentication")
	}
	if cfg.StatePath() != "/var/lib/bitacora/local-auth.json" || cfg.KeyStatePath() != "/var/lib/bitacora/local-auth.key" {
		t.Errorf("paths = %q, %q; want the configured ones", cfg.StatePath(), cfg.KeyStatePath())
	}
}

// A security switch that silently reads as "off" when it is misspelled is
// worse than no switch: the deployment looks configured and is not.
func TestLocalConfigFromEnvRejectsAnUnparseableSwitch(t *testing.T) {
	t.Setenv(EnvLocalAuth, "yes-please")
	if _, err := LocalConfigFromEnv(); err == nil {
		t.Fatal("LocalConfigFromEnv accepted an unparseable switch")
	}
}

func TestLoadLocalStoreStaysInertWhenNeitherConfiguredNorInitialized(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadLocalStore(LocalConfig{
		Path:    filepath.Join(dir, "local-auth.json"),
		KeyPath: filepath.Join(dir, "local-auth.key"),
	})
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	if store != nil {
		t.Fatal("an unconfigured, uninitialized hub must keep local authentication inert")
	}
}

// This is the activation gap the task exists to close: an operator can switch
// the local source on before the credential exists, and the boundary must
// already be live at that point.
func TestLoadLocalStoreIsRequiredAndPendingBeforeInitialization(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadLocalStore(LocalConfig{
		Required: true,
		Path:     filepath.Join(dir, "local-auth.json"),
		KeyPath:  filepath.Join(dir, "local-auth.key"),
	})
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	if store == nil {
		t.Fatal("a configured local source must produce a store before initialization")
	}
	if !store.IsRequired() {
		t.Error("IsRequired() = false, want true")
	}
	if store.IsEnabled() {
		t.Error("IsEnabled() = true before initialization; there is no credential to accept")
	}
	if !store.PendingInitialization() {
		t.Error("PendingInitialization() = false, want true")
	}
}

func TestLoadLocalStoreReportsInitializedCredentialAsEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := LocalConfig{Required: true, Path: filepath.Join(dir, "local-auth.json"), KeyPath: filepath.Join(dir, "local-auth.key")}
	if _, _, err := NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	if store == nil || !store.IsEnabled() || !store.IsRequired() {
		t.Fatalf("store = %v; want an enabled, required store", store)
	}
	if store.PendingInitialization() {
		t.Error("PendingInitialization() = true for an initialized credential")
	}
}

// An existing installation that ran `auth local init` keeps working without
// learning a new environment variable.
func TestLoadLocalStoreKeepsImplicitActivationForAnInitializedCredential(t *testing.T) {
	dir := t.TempDir()
	cfg := LocalConfig{Path: filepath.Join(dir, "local-auth.json"), KeyPath: filepath.Join(dir, "local-auth.key")}
	if _, _, err := NewLocalStore(cfg.StatePath(), cfg.KeyStatePath()).Initialize("correct horse battery staple"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := LoadLocalStore(cfg)
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	if store == nil || !store.IsEnabled() {
		t.Fatal("an initialized credential must stay active without the switch")
	}
}

// A half-written credential is a deployment error, not a disabled source: the
// volume holding the key may simply not be mounted.
func TestLoadLocalStoreRejectsAPartialCredential(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "local-auth.key")
	if err := os.WriteFile(keyPath, make([]byte, localAuthKeyBytes), localAuthFileMode); err != nil {
		t.Fatalf("writing key: %v", err)
	}
	if _, err := LoadLocalStore(LocalConfig{Required: true, Path: filepath.Join(dir, "local-auth.json"), KeyPath: keyPath}); err == nil {
		t.Fatal("LoadLocalStore accepted a credential without its state file")
	}
}

func TestAuthenticateOnAnUninitializedStoreReportsItAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadLocalStore(LocalConfig{Required: true, Path: filepath.Join(dir, "local-auth.json"), KeyPath: filepath.Join(dir, "local-auth.key")})
	if err != nil {
		t.Fatalf("LoadLocalStore: %v", err)
	}
	if _, err := store.Authenticate("anything", "000000"); !errors.Is(err, ErrLocalAuthNotInitialized) {
		t.Fatalf("Authenticate error = %v, want ErrLocalAuthNotInitialized", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading state directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed login created %d file(s); it must never create a credential", len(entries))
	}
}
