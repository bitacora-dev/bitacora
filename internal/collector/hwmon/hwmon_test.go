package hwmon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/capabilities"
	"github.com/bitacora-dev/bitacora/internal/collector"
)

type recordingSink struct{ gauges []gaugeCall }
type gaugeCall struct {
	name   string
	value  float64
	labels collector.Labels
}

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	s.gauges = append(s.gauges, gaugeCall{name: name, value: value, labels: labels})
}
func (*recordingSink) Counter(string, float64, collector.Labels) {}
func (*recordingSink) Event(collector.Event)                     {}
func (*recordingSink) LogLines(string, []collector.LogLine)      {}
func (*recordingSink) Inventory(collector.Inventory)             {}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollector_EmitsReadableCPUSensorsWithStableLabels(t *testing.T) {
	sysRoot := t.TempDir()
	root := filepath.Join(sysRoot, "class", "hwmon")
	writeFixture(t, filepath.Join(root, "hwmon7", "name"), "coretemp\n")
	writeFixture(t, filepath.Join(root, "hwmon7", "temp1_input"), "59000\n")
	writeFixture(t, filepath.Join(root, "hwmon7", "temp1_label"), "Package id 0\n")
	writeFixture(t, filepath.Join(root, "hwmon7", "temp2_input"), "51000\n")
	writeFixture(t, filepath.Join(root, "hwmon7", "temp2_label"), "Core 0\n")
	writeFixture(t, filepath.Join(root, "hwmon7", "temp3_input"), "not-a-temperature\n")
	writeFixture(t, filepath.Join(root, "hwmon2", "name"), "nvme\n")
	writeFixture(t, filepath.Join(root, "hwmon2", "temp1_input"), "42000\n")

	c := New()
	if err := c.Init(context.Background(), collector.Config{"sys_root": sysRoot}, nil); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.gauges) != 2 {
		t.Fatalf("gauges = %#v, want only the two readable CPU sensors", sink.gauges)
	}
	bySensor := map[string]float64{}
	for _, gauge := range sink.gauges {
		if gauge.name != "bitacora_cpu_temperature_celsius" || gauge.labels["chip"] != "coretemp" {
			t.Fatalf("unexpected gauge = %#v", gauge)
		}
		bySensor[gauge.labels["sensor"]] = gauge.value
	}
	if bySensor["package_id_0"] != 59 || bySensor["core_0"] != 51 {
		t.Fatalf("CPU gauges = %#v", bySensor)
	}
	for _, gauge := range sink.gauges {
		if gauge.labels["chip"] == "hwmon7" {
			t.Fatal("hwmon directory number must not be used as metric identity")
		}
	}
}

func TestCollector_NoHwmonProducesNoMetrics(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"sys_root": t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.gauges) != 0 {
		t.Fatalf("gauges = %#v, want none", sink.gauges)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("Collect must stop for an already-cancelled context")
	}
}

func TestCollector_RequiresHwmonCapability(t *testing.T) {
	got := New().Requires()
	if len(got) != 1 || got[0] != capabilities.HwHwmon {
		t.Fatalf("Requires() = %v, want hw.hwmon", got)
	}
}
