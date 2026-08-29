// Package publicsurface implements the VPS public-surface collector from
// ADR-0004. It only reads local logs and operator-provided status snapshots:
// no shelling out, no firewall mutation, no remote API calls.
package publicsurface

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
)

const (
	defaultAuthLogPath        = "/var/log/auth.log"
	defaultSecureLogPath      = "/var/log/secure"
	defaultFail2BanStatusPath = "/var/lib/bitacora/public-surface/fail2ban.json"
	defaultFirewallRulesPath  = "/var/lib/bitacora/public-surface/firewall.rules"
	defaultOVHTrafficPath     = "/var/lib/bitacora/public-surface/ovh-traffic.json"
)

// Collector emits read-only public exposure signals for internet-facing hosts:
// SSH failures, fail2ban jail state, active firewall rules, and OVH traffic
// quota usage when the operator exports it locally.
type Collector struct {
	authLogPath        string
	fail2banStatusPath string
	firewallRulesPath  string
	ovhTrafficPath     string
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "public_surface" }

// Requires implements collector.Collector.
func (c *Collector) Requires() []collector.Capability {
	return []collector.Capability{capabilities.PublicExposed}
}

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.authLogPath = firstConfiguredPath(cfg, "auth_log_path", defaultAuthLogPath, defaultSecureLogPath)
	c.fail2banStatusPath = configuredPath(cfg, "fail2ban_status_path", defaultFail2BanStatusPath)
	c.firewallRulesPath = configuredPath(cfg, "firewall_rules_path", defaultFirewallRulesPath)
	c.ovhTrafficPath = configuredPath(cfg, "ovh_traffic_path", defaultOVHTrafficPath)
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var errs []error
	if failures, err := countSSHFailures(c.authLogPath); err == nil {
		sink.Gauge("bitacora_public_ssh_failed_logins_total", float64(failures), nil)
	} else {
		errs = append(errs, err)
	}

	if status, err := readFail2BanStatus(c.fail2banStatusPath); err == nil {
		sink.Gauge("bitacora_public_fail2ban_jails_total", float64(status.Jails), nil)
		sink.Gauge("bitacora_public_fail2ban_banned_total", float64(status.Banned), nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}

	if rules, err := countFirewallRules(c.firewallRulesPath); err == nil {
		sink.Gauge("bitacora_public_firewall_rules_total", float64(rules), nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}

	if ratio, ok, err := readOVHTrafficRatio(c.ovhTrafficPath); err == nil && ok {
		sink.Gauge("bitacora_public_ovh_traffic_used_ratio", ratio, nil)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func configuredPath(cfg collector.Config, key, fallback string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func firstConfiguredPath(cfg collector.Config, key string, fallbacks ...string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	for _, fallback := range fallbacks {
		if _, err := os.Stat(fallback); err == nil {
			return fallback
		}
	}
	if len(fallbacks) == 0 {
		return ""
	}
	return fallbacks[0]
}

func countSSHFailures(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("reading auth log %s: %w", path, err)
	}
	defer file.Close()

	failures := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Failed password") || strings.Contains(line, "Invalid user") {
			failures++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scanning auth log %s: %w", path, err)
	}
	return failures, nil
}

type fail2banSnapshot struct {
	Jails []struct {
		Name   string `json:"name"`
		Banned int    `json:"banned"`
	} `json:"jails"`
}

type fail2banStatus struct {
	Jails  int
	Banned int
}

func readFail2BanStatus(path string) (fail2banStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fail2banStatus{}, err
	}
	var snapshot fail2banSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fail2banStatus{}, fmt.Errorf("parsing fail2ban status %s: %w", path, err)
	}
	status := fail2banStatus{Jails: len(snapshot.Jails)}
	for _, jail := range snapshot.Jails {
		if jail.Banned > 0 {
			status.Banned += jail.Banned
		}
	}
	return status, nil
}

func countFirewallRules(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	rules := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rules++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scanning firewall rules %s: %w", path, err)
	}
	return rules, nil
}

type ovhTrafficSnapshot struct {
	UsedBytes  float64 `json:"used_bytes"`
	QuotaBytes float64 `json:"quota_bytes"`
}

func readOVHTrafficRatio(path string) (float64, bool, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0, false, err
	}
	var snapshot ovhTrafficSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return 0, false, fmt.Errorf("parsing OVH traffic quota %s: %w", path, err)
	}
	if snapshot.QuotaBytes <= 0 {
		return 0, false, nil
	}
	ratio := snapshot.UsedBytes / snapshot.QuotaBytes
	switch {
	case ratio < 0:
		ratio = 0
	case ratio > 1:
		ratio = 1
	}
	return ratio, true, nil
}
