package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

const (
	containerA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	containerB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type recordingSink struct {
	counters []call
	gauges   []call
}

type call struct {
	name   string
	value  float64
	labels collector.Labels
}

func (s *recordingSink) Gauge(name string, value float64, labels collector.Labels) {
	s.gauges = append(s.gauges, call{name, value, labels})
}
func (s *recordingSink) Counter(name string, value float64, labels collector.Labels) {
	s.counters = append(s.counters, call{name, value, labels})
}
func (s *recordingSink) Event(collector.Event)                {}
func (s *recordingSink) LogLines(string, []collector.LogLine) {}

func TestCollector_ReadsCPUAndMemoryFromCgroupFixtures(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"cgroup_root": "testdata/cgroup"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sink.counters) != 2 {
		t.Fatalf("expected 2 cpu counter samples (one per container), got %d", len(sink.counters))
	}
	if len(sink.gauges) != 2 {
		t.Fatalf("expected 2 memory gauge samples, got %d", len(sink.gauges))
	}

	foundA := false
	for _, c := range sink.counters {
		if c.labels["container_id"] == containerA[:12] {
			foundA = true
			// usage_usec 12345678 -> seconds
			want := 12345678.0 / 1e6
			if diff := c.value - want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("expected cpu seconds %.6f for container A, got %.6f", want, c.value)
			}
		}
	}
	if !foundA {
		t.Fatal("expected a cpu sample for container A")
	}

	for _, g := range sink.gauges {
		if g.labels["container_id"] == containerA[:12] && g.value != 104857600 {
			t.Fatalf("expected memory.current 104857600 for container A, got %v", g.value)
		}
	}
}

func TestCollector_WithoutMetadataFallsBackToTruncatedIDAsName(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"cgroup_root": "testdata/cgroup"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, g := range sink.gauges {
		if g.labels["container_name"] != g.labels["container_id"] {
			t.Fatalf("expected container_name to fall back to the truncated id without a metadata client, got %+v", g.labels)
		}
	}
}

func TestCollector_WithMetadataUsesRealContainerNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]containerListEntry{
			{ID: containerA, Names: []string{"/web"}},
			{ID: containerB, Names: []string{"/db"}},
		})
	}))
	defer server.Close()

	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"cgroup_root":             "testdata/cgroup",
		"docker_socket_proxy_url": server.URL,
	}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotNames := map[string]string{}
	for _, g := range sink.gauges {
		gotNames[g.labels["container_id"]] = g.labels["container_name"]
	}
	if gotNames[containerA[:12]] != "web" {
		t.Fatalf("expected container A named 'web', got %+v", gotNames)
	}
	if gotNames[containerB[:12]] != "db" {
		t.Fatalf("expected container B named 'db', got %+v", gotNames)
	}
}

func TestCollector_MetadataProxyDownDegradesGracefully(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{
		"cgroup_root":             "testdata/cgroup",
		"docker_socket_proxy_url": "http://127.0.0.1:1", // nothing listens here
	}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("expected Collect to succeed even when the metadata proxy is unreachable (degraded mode), got %v", err)
	}
	if len(sink.gauges) != 2 {
		t.Fatalf("expected resource metrics to still be emitted in degraded mode, got %d gauges", len(sink.gauges))
	}
}

func TestCollector_NoContainersIsNotAnError(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"cgroup_root": "testdata/empty-cgroup"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sink := &recordingSink{}
	if err := c.Collect(context.Background(), sink); err != nil {
		t.Fatalf("expected a missing/empty cgroup root not to error, got %v", err)
	}
}

func TestCollector_RespectsContextCancellation(t *testing.T) {
	c := New()
	if err := c.Init(context.Background(), collector.Config{"cgroup_root": "testdata/cgroup"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Collect(ctx, &recordingSink{}); err == nil {
		t.Fatal("expected Collect to return an error for an already-cancelled context")
	}
}
