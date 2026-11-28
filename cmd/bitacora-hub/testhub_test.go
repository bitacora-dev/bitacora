package main

import (
	"path/filepath"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/hubpipeline"
)

// testOperatorPassword initializes the credential newHub creates. It is a
// test fixture, never a default: production has no credential until setup.
const testOperatorPassword = "test-operator-password"

// newHub builds a hub that is past ADR-0025 setup: it carries an initialized
// local credential under dataDir, so device tokens and the dashboard behave as
// on a configured installation. Rebuilding on the same dataDir reuses it.
func newHub(dataDir string, pipelineConfig ...hubpipeline.Config) (*hub, error) {
	store := hubauth.NewLocalStore(filepath.Join(dataDir, "auth", "local-auth.json"), filepath.Join(dataDir, "auth", "local-auth.key"))
	if !store.Initialized() {
		if _, _, err := store.Initialize(testOperatorPassword); err != nil {
			return nil, err
		}
	}
	return newHubWithLocalAuth(dataDir, store, pipelineConfig...)
}

// newSetupModeHub builds a hub with no login source at all: the fail-closed
// state of a fresh installation.
func newSetupModeHub(dataDir string) (*hub, *hubauth.LocalStore, error) {
	store := hubauth.NewLocalStore(filepath.Join(dataDir, "auth", "local-auth.json"), filepath.Join(dataDir, "auth", "local-auth.key"))
	h, err := newHubWithLocalAuth(dataDir, store)
	return h, store, err
}
