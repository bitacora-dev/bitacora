package capabilities

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// fakeRoot builds a Config rooted at a temp directory, with every
// production path preserved (so the "root" join logic under test matches
// DefaultConfig), and creates the given files/dirs beneath it.
func fakeRoot(t *testing.T, files map[string]string, dirs []string) Config {
	t.Helper()
	root := t.TempDir()

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	cfg := DefaultConfig
	cfg.Root = root
	return cfg
}

func TestDetect_AllCapabilitiesUnavailableOnEmptyRoot(t *testing.T) {
	cfg := fakeRoot(t, nil, nil)

	m := Detect(cfg, "01HOST", "myhost", "0.1.0", time.Unix(0, 0).UTC())

	for _, cap := range All {
		status, ok := m.Capabilities[cap]
		if !ok {
			t.Fatalf("manifest missing capability %q", cap)
		}
		if cap == PublicExposed {
			continue // operator-declared, defaults to false either way
		}
		if status.Available {
			t.Errorf("expected %q to be unavailable on an empty root, got %+v", cap, status)
		}
		if status.Reason == "" {
			t.Errorf("expected %q to carry a reason when unavailable", cap)
		}
	}
}

func TestDetect_DetectsPresentCapabilities(t *testing.T) {
	cfg := fakeRoot(t,
		map[string]string{
			"/var/lib/dpkg/status":         "",
			"/proc/mdstat":                 "Personalities : [raid1]\nmd0 : active raid1 sda1[0] sdb1[1]\n",
			"/proc/sys/kernel/osrelease":   "6.8.0-45-generic\n",
			"/etc/os-release":              "ID=ubuntu\nVERSION_ID=\"24.04\"\n",
			"/sys/class/hwmon/hwmon0/name": "coretemp\n",
		},
		[]string{
			"/run/systemd/system",
			"/var/log/journal",
			"/sys/fs/selinux",
		},
	)

	m := Detect(cfg, "01HOST", "myhost", "0.1.0", time.Unix(0, 0).UTC())

	cases := map[collector.Capability]bool{
		InitSystemd:     true,
		LogsJournald:    true,
		PkgApt:          true,
		StorageMdraid:   true,
		HwHwmon:         true,
		SecSelinux:      true,
		PkgDnf:          false,
		ContainerDocker: false,
	}
	for cap, want := range cases {
		got := m.Capabilities[cap].Available
		if got != want {
			t.Errorf("capability %q: got available=%v, want %v (%+v)", cap, got, want, m.Capabilities[cap])
		}
	}

	if m.OS.Kernel != "6.8.0-45-generic" {
		t.Errorf("expected kernel to be parsed, got %q", m.OS.Kernel)
	}
	if m.OS.Distro != "ubuntu" || m.OS.Version != "24.04" {
		t.Errorf("expected distro/version to be parsed, got %q/%q", m.OS.Distro, m.OS.Version)
	}
	if got := m.Capabilities[StorageMdraid].Detail; got != "md0" {
		t.Errorf("expected mdraid detail to list the device, got %q", got)
	}
}

func TestDetect_DegradedListsOnlyKnownImpactfulGaps(t *testing.T) {
	cfg := fakeRoot(t, nil, nil)

	m := Detect(cfg, "01HOST", "myhost", "0.1.0", time.Unix(0, 0).UTC())

	found := map[string]bool{}
	for _, d := range m.Degraded {
		found[d.Capability] = true
		if d.Impact == "" {
			t.Errorf("degraded entry for %q has no impact", d.Capability)
		}
	}
	if !found[string(HwPstore)] {
		t.Error("expected hw.pstore to appear in degraded, it has a documented remedy")
	}
	if found[string(PkgDnf)] {
		t.Error("pkg.dnf missing on a non-RPM host is not a degradation, it shouldn't appear in degraded")
	}
}

func TestManifest_AvailableMapFeedsRegistryResolve(t *testing.T) {
	cfg := fakeRoot(t, nil, []string{"/run/systemd/system"})
	m := Detect(cfg, "01HOST", "myhost", "0.1.0", time.Now())

	available := m.Available()
	if !available[InitSystemd] {
		t.Fatal("expected init.systemd to be available in the reduced map")
	}
	if available[PkgDnf] {
		t.Fatal("expected pkg.dnf to be absent/false in the reduced map")
	}
}
