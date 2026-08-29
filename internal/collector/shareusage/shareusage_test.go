package shareusage

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

func TestDirSize_SumsRealFilesRecursively(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "12345")             // 5 bytes
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "1234567890") // 10 bytes

	size, err := dirSize(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 15 {
		t.Fatalf("expected 15 bytes total, got %d", size)
	}
}

func TestDirSize_MissingPathIsAnError(t *testing.T) {
	_, err := dirSize(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error walking a nonexistent path")
	}
}

func TestDirSize_RespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	// Enough genuinely distinct files that the walk's every-100-entries
	// cancellation check is guaranteed to run before it finishes.
	for i := 0; i < 500; i++ {
		writeFile(t, filepath.Join(dir, strconv.Itoa(i)+".txt"), "x")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dirSize(ctx, dir)
	if err == nil {
		t.Fatal("expected the walk to be cancelled")
	}
}

func TestCollector_MeasuresConfiguredShares(t *testing.T) {
	dir := t.TempDir()
	sambaConf := filepath.Join(dir, "smb.conf")
	shareDir := filepath.Join(dir, "multimedia")

	writeFile(t, sambaConf, "[multimedia]\n\tpath = "+shareDir+"\n")
	writeFile(t, filepath.Join(shareDir, "movie.mp4"), "0123456789") // 10 bytes

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   sambaConf,
		"exports_file": filepath.Join(dir, "no-exports"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 || sink.inventories[0].Kind != schema.InventoryShareUsage {
		t.Fatalf("unexpected inventory: %+v", sink.inventories)
	}
	items := sink.inventories[0].Items
	if len(items) != 1 || items[0].ID != "multimedia" {
		t.Fatalf("expected 1 measured share, got %+v", items)
	}
	if items[0].Attrs["used_bytes"] != "10" {
		t.Fatalf("expected used_bytes=10, got %q", items[0].Attrs["used_bytes"])
	}
	if items[0].Attrs["calculated_at"] == "" {
		t.Fatal("expected calculated_at to be set")
	}
}

func TestCollector_OneShareFailingDoesNotBlockOthers(t *testing.T) {
	dir := t.TempDir()
	sambaConf := filepath.Join(dir, "smb.conf")
	goodShareDir := filepath.Join(dir, "good")
	writeFile(t, sambaConf,
		"[missing]\n\tpath = "+filepath.Join(dir, "does-not-exist")+"\n\n"+
			"[good]\n\tpath = "+goodShareDir+"\n")
	writeFile(t, filepath.Join(goodShareDir, "f.txt"), "12345") // 5 bytes

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"samba_conf":   sambaConf,
		"exports_file": filepath.Join(dir, "no-exports"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := sink.inventories[0].Items
	if len(items) != 1 || items[0].ID != "good" {
		t.Fatalf("expected only the good share measured, got %+v", items)
	}
}

func TestCollector_NoSharesYieldsEmptySnapshotNotError(t *testing.T) {
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
