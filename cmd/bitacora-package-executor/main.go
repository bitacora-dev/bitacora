// bitacora-package-executor is a short-lived, root, systemd-path-triggered
// ADR-0022 helper. It maps two operation names to two fixed apt-get argv lists.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/bitacora-dev/bitacora/internal/packageexecutor"
)

const actionsFile = "/etc/bitacora/actions.json"

func main() {
	allowlist, maxAge, err := loadConfig(actionsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bitacora-package-executor:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	_, err = packageexecutor.ProcessAll(ctx, packageexecutor.Config{Allowlist: allowlist, MaxCacheAge: maxAge}, run)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bitacora-package-executor:", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (packageexecutor.Allowlist, time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageexecutor.Allowlist{}, 0, fmt.Errorf("reading local action configuration: %w", err)
	}
	var config struct {
		RefreshPackageCache        bool `json:"refresh_package_cache"`
		ApplyPendingPackageUpdates bool `json:"apply_pending_package_updates"`
		PackageCacheMaxAgeSeconds  int  `json:"package_cache_max_age_seconds"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return packageexecutor.Allowlist{}, 0, fmt.Errorf("decoding local action configuration: %w", err)
	}
	maxAge := packageexecutor.DefaultMaxCacheAge
	if config.PackageCacheMaxAgeSeconds > 0 {
		maxAge = time.Duration(config.PackageCacheMaxAgeSeconds) * time.Second
	}
	return packageexecutor.Allowlist{RefreshPackageCache: config.RefreshPackageCache, ApplyPendingPackageUpdates: config.ApplyPendingPackageUpdates}, maxAge, nil
}

// run is the only privileged command dispatch in this helper. No request data
// can change these argv values, paths, or package selection.
func run(ctx context.Context, operation packageexecutor.Operation) ([]byte, int, error) {
	var command *exec.Cmd
	switch operation {
	case packageexecutor.RefreshPackageCache:
		command = exec.CommandContext(ctx, "/usr/bin/apt-get", "update")
	case packageexecutor.ApplyPendingPackageUpdates:
		command = exec.CommandContext(ctx, "/usr/bin/apt-get", "-y", "upgrade")
	default:
		return nil, 1, errors.New("operation is not executable")
	}
	output, err := command.CombinedOutput()
	if err == nil {
		return output, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), err
	}
	return output, 1, err
}
