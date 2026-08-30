package diskarray

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
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

func TestReadRealMounts_FiltersPseudoFilesystemsAndUnescapes(t *testing.T) {
	dir := t.TempDir()
	mountsFile := filepath.Join(dir, "mounts")
	writeFile(t, mountsFile,
		"proc /proc proc rw 0 0\n"+
			"tmpfs /run tmpfs rw 0 0\n"+
			"/dev/sda1 / ext4 rw 0 0\n"+
			"/dev/sdb1 /mnt/my\\040drive xfs rw 0 0\n",
	)

	mounts, err := readRealMounts(mountsFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 2 {
		t.Fatalf("expected 2 real mounts, got %d: %+v", len(mounts), mounts)
	}
	if mounts[0].device != "/dev/sda1" || mounts[0].mountpoint != "/" {
		t.Fatalf("unexpected first mount: %+v", mounts[0])
	}
	if mounts[1].mountpoint != "/mnt/my drive" {
		t.Fatalf("expected the escaped space to be decoded, got %q", mounts[1].mountpoint)
	}
}

func TestBaseDeviceName(t *testing.T) {
	cases := map[string]string{
		"/dev/sda1":      "sda",
		"/dev/sdc":       "sdc",
		"/dev/nvme0n1p1": "nvme0n1",
		"/dev/nvme0n1":   "nvme0n1",
		"/dev/md0":       "md0",
	}
	for input, want := range cases {
		if got := baseDeviceName(input); got != want {
			t.Errorf("baseDeviceName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStatfsUsage_RealMountpoint(t *testing.T) {
	dir := t.TempDir()
	usage, ok := statfsUsage(dir)
	if !ok {
		t.Fatal("expected statfs to succeed against a real, existing directory")
	}
	if usage.total == 0 {
		t.Fatal("expected a non-zero total capacity from a real filesystem")
	}
	if usage.used > usage.total {
		t.Fatalf("used (%d) must not exceed total (%d)", usage.used, usage.total)
	}
}

func TestStatfsUsage_MissingPathFails(t *testing.T) {
	_, ok := statfsUsage("/this/path/does/not/exist/anywhere")
	if ok {
		t.Fatal("expected statfs on a nonexistent path to fail")
	}
}

func TestCollector_CombinesMountsAndSMARTIdentity(t *testing.T) {
	dir := t.TempDir()
	mountsFile := filepath.Join(dir, "mounts")
	spoolDir := filepath.Join(dir, "spool")
	realMount := t.TempDir() // a real, statfs-able directory

	writeFile(t, mountsFile, "/dev/sdc1 "+realMount+" ext4 rw 0 0\n")

	smartData := map[string]any{
		"devices": map[string]any{
			"sdc": map[string]string{"model_name": "ST18000NM004J", "serial_number": "ZR5D6B1Z"},
		},
	}
	if err := spool.WriteAtomic(spoolDir, "smart", 1, smartData, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"mounts_file": mountsFile,
		"spool_dir":   spoolDir,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 || sink.inventories[0].Kind != schema.InventoryDisk {
		t.Fatalf("unexpected inventory: %+v", sink.inventories)
	}
	items := sink.inventories[0].Items
	if len(items) != 1 {
		t.Fatalf("expected 1 disk item, got %d: %+v", len(items), items)
	}

	attrs := items[0].Attrs
	if attrs["device"] != "/dev/sdc1" {
		t.Fatalf("unexpected device: %q", attrs["device"])
	}
	if attrs["model"] != "ST18000NM004J" || attrs["serial"] != "ZR5D6B1Z" {
		t.Fatalf("expected SMART identity matched by base device name, got %+v", attrs)
	}
	if attrs["capacity_bytes"] == "" || attrs["used_bytes"] == "" {
		t.Fatalf("expected real statfs usage attrs, got %+v", attrs)
	}
}

func TestCollector_NoSMARTSpoolStillReportsMounts(t *testing.T) {
	dir := t.TempDir()
	mountsFile := filepath.Join(dir, "mounts")
	realMount := t.TempDir()
	writeFile(t, mountsFile, "/dev/sdz1 "+realMount+" ext4 rw 0 0\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"mounts_file": mountsFile,
		"spool_dir":   filepath.Join(dir, "spool"), // never written
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items := sink.inventories[0].Items
	if len(items) != 1 {
		t.Fatalf("expected 1 disk item even without SMART data, got %d", len(items))
	}
	if _, ok := items[0].Attrs["model"]; ok {
		t.Fatalf("expected no model attr without SMART data, got %+v", items[0].Attrs)
	}
}

func TestCollector_MissingMountsFileYieldsEmptySnapshotNotError(t *testing.T) {
	dir := t.TempDir()
	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"mounts_file": filepath.Join(dir, "no-mounts"),
		"spool_dir":   filepath.Join(dir, "spool"),
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
