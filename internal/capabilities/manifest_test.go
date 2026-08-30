package capabilities

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// TestManifest_JSONMatchesDocumentedShape locks the wire format to the
// example in docs/adr/0004-multihost-y-manifiesto-de-capacidades.md: the
// manifest is a contract (ADR-0004 "Negativas") and a field rename here is a
// breaking change for the hub.
func TestManifest_JSONMatchesDocumentedShape(t *testing.T) {
	m := Manifest{
		HostID:       "01J8X000000000000000000000",
		Hostname:     "icloudserver",
		ReportedAt:   time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
		AgentVersion: "0.3.1",
		OS: OSInfo{
			Family:  "linux",
			Distro:  "ubuntu",
			Version: "24.04",
			Kernel:  "6.8.0-45-generic",
			Arch:    "amd64",
		},
		Capabilities: map[collector.Capability]Status{
			InitSystemd: {Available: true, Detail: "257"},
			HwPstore:    {Available: false, Reason: "ramoops not configured"},
		},
		Degraded: []Degraded{
			{Capability: "hw.pstore", Impact: "no se podrá diagnosticar un cuelgue duro", Remedy: "docs/setup/ramoops.md"},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, field := range []string{"host_id", "hostname", "reported_at", "agent_version", "os", "capabilities", "degraded"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("missing top-level field %q in manifest JSON", field)
		}
	}

	osFields, ok := decoded["os"].(map[string]any)
	if !ok {
		t.Fatal("os field is not an object")
	}
	for _, field := range []string{"family", "distro", "version", "kernel", "arch"} {
		if _, ok := osFields[field]; !ok {
			t.Errorf("missing os field %q", field)
		}
	}

	caps, ok := decoded["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities field is not an object")
	}
	systemd, ok := caps["init.systemd"].(map[string]any)
	if !ok {
		t.Fatal(`capabilities["init.systemd"] is not an object`)
	}
	if available, _ := systemd["available"].(bool); !available {
		t.Error("expected init.systemd.available to be true")
	}
	if detail, _ := systemd["detail"].(string); detail != "257" {
		t.Errorf("expected init.systemd.detail to be %q, got %q", "257", detail)
	}

	degraded, ok := decoded["degraded"].([]any)
	if !ok || len(degraded) != 1 {
		t.Fatalf("expected one degraded entry, got %+v", decoded["degraded"])
	}
	entry := degraded[0].(map[string]any)
	if entry["capability"] != "hw.pstore" || entry["remedy"] != "docs/setup/ramoops.md" {
		t.Errorf("unexpected degraded entry: %+v", entry)
	}
}

func TestManifest_OmitsEmptyOptionalFields(t *testing.T) {
	m := Manifest{
		HostID:       "01J8X000000000000000000000",
		Hostname:     "vps",
		ReportedAt:   time.Now(),
		AgentVersion: "0.3.1",
		OS:           OSInfo{Family: "linux", Arch: "amd64"},
		Capabilities: map[collector.Capability]Status{
			PkgApt: {Available: false, Reason: "dpkg status file not found"},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"degraded"`) {
		t.Error("expected degraded to be omitted when empty")
	}
	if strings.Contains(string(data), `"distro"`) {
		t.Error("expected os.distro to be omitted when empty")
	}
}
