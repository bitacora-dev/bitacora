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

func TestCollector_AnnotatesMDRAIDAndSnapRAIDMembers(t *testing.T) {
	dir := t.TempDir()
	rootMount := t.TempDir()
	degradedMount := t.TempDir()
	dataMount := t.TempDir()
	parityMount := t.TempDir()
	standaloneMount := t.TempDir()
	mountsFile := filepath.Join(dir, "mounts")
	mdstatFile := filepath.Join(dir, "mdstat")
	snapraidConf := filepath.Join(dir, "snapraid.conf")
	writeFile(t, mountsFile,
		"/dev/md0 "+rootMount+" ext4 rw 0 0\n"+
			"/dev/sdb1 "+dataMount+" ext4 rw 0 0\n"+
			"/dev/sdc1 "+parityMount+" ext4 rw 0 0\n"+
			"/dev/sdd1 "+standaloneMount+" ext4 rw 0 0\n"+
			"/dev/md1 "+degradedMount+" ext4 rw 0 0\n")
	writeFile(t, mdstatFile, `Personalities : [raid1] [raid5]
md0 : active raid1 nvme0n1p2[0] nvme1n1p2[1]
      976630336 blocks super 1.2 [2/2] [UU]
md1 : active raid5 sda1[0] sdb1[1] sdc1[2](F)
      3906762752 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [UU_]
unused devices: <none>
`)
	writeFile(t, snapraidConf, "# representative SnapRAID configuration\n"+
		"parity "+parityMount+"/snapraid.parity\n"+
		"parity 2 /mnt/second-parity/snapraid.parity\n"+
		"data disk-a "+dataMount+"/\n"+
		"data disk-b /mnt/second-data/\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"mounts_file":   mountsFile,
		"spool_dir":     filepath.Join(dir, "spool"),
		"mdstat_file":   mdstatFile,
		"snapraid_conf": snapraidConf,
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := sink.inventories[0].Items
	byDevice := make(map[string]schema.Labels, len(items))
	for _, item := range items {
		byDevice[item.Attrs["device"]] = item.Attrs
	}
	if got := byDevice["/dev/md0"]; got["array_type"] != "storage.mdraid" || got["array_level"] != "raid1" || got["array_member_count"] != "2" || got["array_health"] != "healthy" {
		t.Fatalf("unexpected healthy mdraid attrs: %+v", got)
	}
	if got := byDevice["/dev/md1"]; got["array_type"] != "storage.mdraid" || got["array_level"] != "raid5" || got["array_member_count"] != "3" || got["array_health"] != "degraded" {
		t.Fatalf("unexpected degraded mdraid attrs: %+v", got)
	}
	for _, device := range []string{"/dev/sdb1", "/dev/sdc1"} {
		got := byDevice[device]
		if got["array_type"] != "storage.snapraid" || got["array_level"] != "2 parity disks" || got["array_member_count"] != "4" || got["array_health"] != "unknown" {
			t.Fatalf("unexpected SnapRAID attrs for %s: %+v", device, got)
		}
	}
	if got := byDevice["/dev/sdd1"]; got["array_type"] != "" || got["array_level"] != "" || got["array_member_count"] != "" || got["array_health"] != "" {
		t.Fatalf("standalone disk gained array attrs: %+v", got)
	}
}

func TestCollector_MissingArraySourcesStillReportsMounts(t *testing.T) {
	dir := t.TempDir()
	mount := t.TempDir()
	mountsFile := filepath.Join(dir, "mounts")
	writeFile(t, mountsFile, "/dev/sdz1 "+mount+" ext4 rw 0 0\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"mounts_file":   mountsFile,
		"spool_dir":     filepath.Join(dir, "spool"),
		"mdstat_file":   filepath.Join(dir, "missing-mdstat"),
		"snapraid_conf": filepath.Join(dir, "missing-snapraid.conf"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.inventories) != 1 || len(sink.inventories[0].Items) != 1 {
		t.Fatalf("expected disk inventory despite missing array sources, got %+v", sink.inventories)
	}
	if attrs := sink.inventories[0].Items[0].Attrs; attrs["array_type"] != "" {
		t.Fatalf("missing sources must not add array attrs: %+v", attrs)
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
