package users

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

type recordingSink struct {
	inventories []schema.Inventory
}

func (s *recordingSink) Gauge(string, float64, collector.Labels)   {}
func (s *recordingSink) Counter(string, float64, collector.Labels) {}
func (s *recordingSink) Event(collector.Event)                     {}
func (s *recordingSink) LogLines(string, []collector.LogLine)      {}
func (s *recordingSink) Inventory(inv collector.Inventory) {
	s.inventories = append(s.inventories, inv)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

const passwdFixture = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
nacho:x:1000:1000:Nacho:/home/nacho:/bin/bash
backup-agent:x:1001:1001:Backup Agent:/home/backup-agent:/bin/bash
`

const sambaConfFixture = `[global]
	workgroup = WORKGROUP

[multimedia]
	path = /mnt/user/multimedia
	valid users = nacho backup-agent
	write list = nacho

[isos]
	path = /mnt/user/isos
	valid users = nacho
`

func setupCollector(t *testing.T, dir string, loginDefs string) *Collector {
	t.Helper()
	passwdFile := filepath.Join(dir, "passwd")
	sambaConf := filepath.Join(dir, "smb.conf")
	writeFile(t, passwdFile, passwdFixture)
	writeFile(t, sambaConf, sambaConfFixture)

	loginDefsPath := filepath.Join(dir, "login.defs")
	if loginDefs != "" {
		writeFile(t, loginDefsPath, loginDefs)
	}

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"passwd_file": passwdFile,
		"login_defs":  loginDefsPath,
		"samba_conf":  sambaConf,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return c
}

func TestCollector_ExcludesSystemAccounts(t *testing.T) {
	dir := t.TempDir()
	c := setupCollector(t, dir, "")

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inv := sink.inventories[0]
	if inv.HostID != "host-a" || inv.Kind != schema.InventoryUser {
		t.Fatalf("unexpected inventory header: %+v", inv)
	}
	// root (uid 0), daemon (uid 1), nobody (uid 65534) must all be excluded.
	if len(inv.Items) != 2 {
		t.Fatalf("expected 2 real users, got %d: %+v", len(inv.Items), inv.Items)
	}

	names := map[string]bool{}
	for _, item := range inv.Items {
		names[item.ID] = true
	}
	if !names["nacho"] || !names["backup-agent"] {
		t.Fatalf("expected nacho and backup-agent, got %+v", names)
	}
	if names["root"] || names["daemon"] || names["nobody"] {
		t.Fatalf("expected system accounts excluded, got %+v", names)
	}
}

func TestCollector_ResolvesSambaSharePermissions(t *testing.T) {
	dir := t.TempDir()
	c := setupCollector(t, dir, "")

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byID := map[string]schema.InventoryItem{}
	for _, item := range sink.inventories[0].Items {
		byID[item.ID] = item
	}

	nacho := byID["nacho"]
	if nacho.Attrs["uid"] != "1000" {
		t.Fatalf("expected uid 1000, got %q", nacho.Attrs["uid"])
	}
	// nacho: write list on multimedia -> rw; valid users on both shares -> ro
	// includes multimedia too (read access is a superset of write access).
	if nacho.Attrs["shares_rw"] != "multimedia" {
		t.Fatalf("expected nacho to have write access to multimedia, got %q", nacho.Attrs["shares_rw"])
	}
	roShares := nacho.Attrs["shares_ro"]
	if !strings.Contains(roShares, "multimedia") || !strings.Contains(roShares, "isos") {
		t.Fatalf("expected nacho to have read access to both multimedia and isos, got %q", roShares)
	}

	backupAgent := byID["backup-agent"]
	if backupAgent.Attrs["shares_rw"] != "" {
		t.Fatalf("expected backup-agent to have no write access, got %q", backupAgent.Attrs["shares_rw"])
	}
	if backupAgent.Attrs["shares_ro"] != "multimedia" {
		t.Fatalf("expected backup-agent read-only on multimedia, got %q", backupAgent.Attrs["shares_ro"])
	}
}

func TestUIDMin_ReadsLoginDefs(t *testing.T) {
	dir := t.TempDir()
	c := setupCollector(t, dir, "# comment\nUID_MIN\t\t\t  500\nUID_MAX\t\t\t60000\n")

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With UID_MIN=500, "daemon" (uid 1) is still excluded, but nothing
	// new should appear since the fixture has no user in [500,999].
	if len(sink.inventories[0].Items) != 2 {
		t.Fatalf("expected still 2 users with UID_MIN=500, got %d", len(sink.inventories[0].Items))
	}
}

func TestCollector_MissingPasswdYieldsEmptySnapshotNotError(t *testing.T) {
	dir := t.TempDir()
	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"passwd_file": filepath.Join(dir, "no-passwd"),
		"login_defs":  filepath.Join(dir, "no-login-defs"),
		"samba_conf":  filepath.Join(dir, "no-smb.conf"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 1 || len(sink.inventories[0].Items) != 0 {
		t.Fatalf("expected an empty (not missing) snapshot, got %+v", sink.inventories)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected cancellation error")
	}
}
