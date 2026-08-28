package schema

import (
	"testing"
	"time"
)

func validMetric() Metric {
	return Metric{
		Name:      "bitacora_cpu_seconds_total",
		HostID:    "01J8XQZZZZZZZZZZZZZZZZZZZZ",
		Labels:    Labels{"cpu": "0"},
		Value:     12.5,
		Timestamp: time.Now(),
	}
}

func TestMetric_ValidateAcceptsWellFormedMetric(t *testing.T) {
	if err := validMetric().Validate(); err != nil {
		t.Fatalf("expected valid metric to pass, got %v", err)
	}
}

func TestMetric_ValidateRejectsMissingPrefix(t *testing.T) {
	m := validMetric()
	m.Name = "cpu_seconds_total"
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for missing bitacora_ prefix")
	}
}

func TestMetric_ValidateRejectsUnknownSuffix(t *testing.T) {
	m := validMetric()
	m.Name = "bitacora_cpu_widgets"
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for unrecognized unit suffix")
	}
}

func TestMetric_ValidateRejectsMissingHostID(t *testing.T) {
	m := validMetric()
	m.HostID = ""
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for missing host_id")
	}
}

func TestMetric_ValidateRejectsRatioOutOfBounds(t *testing.T) {
	m := validMetric()
	m.Name = "bitacora_cpu_usage_ratio"
	m.Value = 87 // a percentage, not a 0-1 ratio
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for _ratio value outside 0-1")
	}
}

func TestMetric_ValidateRejectsPIDLabel(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{"pid": "1234"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for pid label")
	}
}

func TestMetric_ValidateRejectsFullContainerID(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{
		"container_id":   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85",
		"container_name": "web",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for untruncated container_id")
	}
}

func TestMetric_ValidateRejectsContainerIDWithoutName(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{"container_id": "e3b0c44298fc"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error when container_id has no accompanying container_name")
	}
}

func TestMetric_ValidateAcceptsTruncatedContainerID(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{"container_id": "e3b0c44298fc", "container_name": "web"}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected truncated container_id with name to pass, got %v", err)
	}
}

func TestMetric_ValidateRejectsClientIPLabel(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{"remote": "203.0.113.5"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for a label value that looks like an IP address")
	}
}

func TestMetric_ValidateRejectsFilePathLabel(t *testing.T) {
	m := validMetric()
	m.Labels = Labels{"file": "/var/log/syslog/foo"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for a label value that looks like a file path")
	}
}

func TestMetric_ValidateRejectsMissingTimestamp(t *testing.T) {
	m := validMetric()
	m.Timestamp = time.Time{}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for missing timestamp")
	}
}
