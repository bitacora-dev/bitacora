package pkgupdates

import (
	"context"
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

func TestCollector_CombinesEverySourceIntoOneSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "status"), dpkgStatusFixture)
	writeFile(t, filepath.Join(dir, "lists", "repo_Packages"), aptListsFixtureA)

	dnfData := struct {
		Updates []dnfSpoolUpdate `json:"updates"`
	}{Updates: []dnfSpoolUpdate{{Name: "kernel", Arch: "x86_64", Version: "5.14.0-284.el9", Repo: "baseos"}}}
	if err := spool.WriteAtomic(filepath.Join(dir, "spool"), "dnfupdates", 1, dnfData, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"dpkg_status":        filepath.Join(dir, "status"),
		"apt_lists_dir":      filepath.Join(dir, "lists"),
		"spool_dir":          filepath.Join(dir, "spool"),
		"unraid_plugins_dir": filepath.Join(dir, "no-plugins"),
	}, &collector.HostInfo{ID: "host-a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.inventories) != 1 || sink.inventories[0].Kind != schema.InventoryPackageUpdate {
		t.Fatalf("unexpected inventory: %+v", sink.inventories)
	}

	sources := map[string]bool{}
	for _, item := range sink.inventories[0].Items {
		sources[item.Attrs["source"]] = true
	}
	if !sources["apt"] {
		t.Errorf("expected an apt-sourced item, got items: %+v", sink.inventories[0].Items)
	}
	if !sources["dnf"] {
		t.Errorf("expected a dnf-sourced item, got items: %+v", sink.inventories[0].Items)
	}
}

func TestCollector_NoSourcesYieldsEmptySnapshotNotError(t *testing.T) {
	dir := t.TempDir()
	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"dpkg_status":        filepath.Join(dir, "no-status"),
		"apt_lists_dir":      filepath.Join(dir, "no-lists"),
		"spool_dir":          filepath.Join(dir, "no-spool"),
		"unraid_plugins_dir": filepath.Join(dir, "no-plugins"),
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
