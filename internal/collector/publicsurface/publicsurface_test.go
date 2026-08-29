package publicsurface

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
)

type recordingSink struct {
	gauges map[string]float64
}

func newRecordingSink() *recordingSink { return &recordingSink{gauges: map[string]float64{}} }

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	s.gauges[name] = value
}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}

func TestCollector_RequiresPublicExposureCapability(t *testing.T) {
	c := New()
	req := c.Requires()
	if len(req) != 1 || req[0] != capabilities.PublicExposed {
		t.Fatalf("expected public.exposed requirement, got %+v", req)
	}
}

func TestCollector_ReadsPublicSurfaceFixtures(t *testing.T) {
	dir := t.TempDir()
	authLog := filepath.Join(dir, "auth.log")
	fail2ban := filepath.Join(dir, "fail2ban.json")
	firewall := filepath.Join(dir, "firewall.rules")
	traffic := filepath.Join(dir, "ovh-traffic.json")

	writeFile(t, authLog, `
Aug 29 10:00:00 vps sshd[10]: Failed password for invalid user root from 203.0.113.10 port 22 ssh2
Aug 29 10:00:01 vps sshd[11]: Accepted publickey for admin from 198.51.100.20 port 22 ssh2
Aug 29 10:00:02 vps sshd[12]: Invalid user test from 203.0.113.11 port 22
`)
	writeFile(t, fail2ban, `{"jails":[{"name":"sshd","banned":2},{"name":"recidive","banned":1}]}`)
	writeFile(t, firewall, `
# inet filter
add rule inet filter input ct state established accept
add rule inet filter input tcp dport 22 accept
`)
	writeFile(t, traffic, `{"used_bytes":250,"quota_bytes":1000}`)

	c := New()
	err := c.Init(context.Background(), collector.Config{
		"auth_log_path":        authLog,
		"fail2ban_status_path": fail2ban,
		"firewall_rules_path":  firewall,
		"ovh_traffic_path":     traffic,
	}, nil)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	sink := newRecordingSink()
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("collect: %v", err)
	}

	cases := map[string]float64{
		"bitacora_public_ssh_failed_logins_total": 2,
		"bitacora_public_fail2ban_jails_total":    2,
		"bitacora_public_fail2ban_banned_total":   3,
		"bitacora_public_firewall_rules_total":    2,
		"bitacora_public_ovh_traffic_used_ratio":  0.25,
	}
	for name, want := range cases {
		if got := sink.gauges[name]; got != want {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, newRecordingSink()); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
