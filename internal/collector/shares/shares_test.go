package shares

import (
	"context"
	"os"
	"path/filepath"
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

const sambaConfFixture = `[global]
	workgroup = WORKGROUP
	server string = mediaserver

[multimedia]
	path = /mnt/user/multimedia
	comment = Media library
	public = no
	writeable = yes

[isos]
	path = /mnt/user/isos
	public = no
	read only = yes

[public-share]
	path = /mnt/user/public
	public = yes
	writeable = no
`

func TestCollector_ParsesSambaShares(t *testing.T) {
	dir := t.TempDir()
	sambaConf := filepath.Join(dir, "smb.conf")
	writeFile(t, sambaConf, sambaConfFixture)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   sambaConf,
		"exports_file": filepath.Join(dir, "does-not-exist"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 {
		t.Fatalf("expected 1 inventory snapshot, got %d", len(sink.inventories))
	}
	inv := sink.inventories[0]
	if inv.HostID != "host-a" || inv.Kind != schema.InventoryShare {
		t.Fatalf("unexpected inventory header: %+v", inv)
	}
	// [global] must never appear as a share.
	if len(inv.Items) != 3 {
		t.Fatalf("expected 3 real shares (global excluded), got %d: %+v", len(inv.Items), inv.Items)
	}

	byID := map[string]schema.InventoryItem{}
	for _, item := range inv.Items {
		byID[item.ID] = item
	}

	multimedia, ok := byID["multimedia"]
	if !ok {
		t.Fatal("expected a multimedia share")
	}
	if multimedia.Attrs["path"] != "/mnt/user/multimedia" || multimedia.Attrs["mode"] != "private" || multimedia.Attrs["writable"] != "true" {
		t.Fatalf("unexpected multimedia attrs: %+v", multimedia.Attrs)
	}
	if multimedia.Attrs["protocol"] != "smb" {
		t.Fatalf("expected protocol smb, got %v", multimedia.Attrs["protocol"])
	}

	isos, ok := byID["isos"]
	if !ok {
		t.Fatal("expected an isos share")
	}
	if isos.Attrs["writable"] != "false" {
		t.Fatalf("expected isos to be read-only, got %+v", isos.Attrs)
	}

	pub, ok := byID["public-share"]
	if !ok {
		t.Fatal("expected a public-share")
	}
	if pub.Attrs["mode"] != "public" {
		t.Fatalf("expected public-share mode=public, got %+v", pub.Attrs)
	}
}

const exportsFixture = `# /etc/exports
/srv/nfs/backups 192.168.1.0/24(rw,sync,no_subtree_check)
/srv/nfs/public *(ro,sync)
`

func TestCollector_ParsesNFSExports(t *testing.T) {
	dir := t.TempDir()
	exportsFile := filepath.Join(dir, "exports")
	writeFile(t, exportsFile, exportsFixture)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   filepath.Join(dir, "does-not-exist"),
		"exports_file": exportsFile,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inv := sink.inventories[0]
	if len(inv.Items) != 2 {
		t.Fatalf("expected 2 exports, got %d: %+v", len(inv.Items), inv.Items)
	}

	byID := map[string]schema.InventoryItem{}
	for _, item := range inv.Items {
		byID[item.ID] = item
	}

	backups, ok := byID["/srv/nfs/backups"]
	if !ok {
		t.Fatal("expected the backups export")
	}
	if backups.Attrs["mode"] != "private" || backups.Attrs["writable"] != "true" || backups.Attrs["protocol"] != "nfs" {
		t.Fatalf("unexpected backups attrs: %+v", backups.Attrs)
	}

	public, ok := byID["/srv/nfs/public"]
	if !ok {
		t.Fatal("expected the public export")
	}
	if public.Attrs["mode"] != "public" || public.Attrs["writable"] != "false" {
		t.Fatalf("unexpected public export attrs: %+v", public.Attrs)
	}
}

func TestCollector_CombinesSMBAndNFSInOneSnapshot(t *testing.T) {
	dir := t.TempDir()
	sambaConf := filepath.Join(dir, "smb.conf")
	exportsFile := filepath.Join(dir, "exports")
	writeFile(t, sambaConf, sambaConfFixture)
	writeFile(t, exportsFile, exportsFixture)

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   sambaConf,
		"exports_file": exportsFile,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 {
		t.Fatalf("expected exactly one combined snapshot, got %d", len(sink.inventories))
	}
	if len(sink.inventories[0].Items) != 5 {
		t.Fatalf("expected 3 SMB + 2 NFS = 5 items, got %d", len(sink.inventories[0].Items))
	}
}

func TestCollector_MissingFilesYieldEmptySnapshotNotError(t *testing.T) {
	dir := t.TempDir()

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   filepath.Join(dir, "no-smb.conf"),
		"exports_file": filepath.Join(dir, "no-exports"),
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

func TestCollector_RequiresIsEmpty(t *testing.T) {
	// The collector self-gates on whichever of SMB/NFS actually exists
	// (Registry.Requires() is AND-only, and "at least one of" can't be
	// expressed that way) — see the doc comment on Requires.
	c := New()
	if req := c.Requires(); len(req) != 0 {
		t.Fatalf("expected no hard capability requirement, got %+v", req)
	}
}
