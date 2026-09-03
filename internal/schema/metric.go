// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// requiredPrefix is mandatory on every metric name (ADR-0006).
const requiredPrefix = "bitacora_"

// allowedUnitSuffixes are the SI-derived suffixes ADR-0006 lists as the
// obligatory convention. Extending this list needs a matching ADR note, not
// a silent addition, since it's a public naming contract.
var allowedUnitSuffixes = []string{"_bytes", "_seconds", "_celsius", "_ratio", "_total"}

// forbiddenLabelKeys can never appear on a metric: they blow up cardinality
// or leak unstable identity (ADR-0006).
var forbiddenLabelKeys = map[string]bool{
	"pid":  true,
	"path": true,
}

// Metric is one Prometheus-model sample: name{labels} = value @ timestamp.
type Metric struct {
	Name      string
	HostID    string
	Labels    Labels
	Value     float64
	Timestamp time.Time
}

// Validate enforces the ADR-0006 naming and labeling conventions. It does
// not enforce the cross-metric cardinality budget — see CardinalityTracker
// for that, since it depends on every other series already seen for a host.
func (m Metric) Validate() error {
	if m.HostID == "" {
		return fmt.Errorf("metric %q: host_id is required", m.Name)
	}
	if !strings.HasPrefix(m.Name, requiredPrefix) {
		return fmt.Errorf("metric %q: name must start with %q", m.Name, requiredPrefix)
	}

	suffix := ""
	for _, s := range allowedUnitSuffixes {
		if strings.HasSuffix(m.Name, s) {
			suffix = s
			break
		}
	}
	if suffix == "" {
		return fmt.Errorf("metric %q: name must end in one of %v", m.Name, allowedUnitSuffixes)
	}
	if suffix == "_ratio" && (m.Value < 0 || m.Value > 1) {
		return fmt.Errorf("metric %q: _ratio value must be 0-1, never a percentage, got %v", m.Name, m.Value)
	}

	if m.Timestamp.IsZero() {
		return fmt.Errorf("metric %q: timestamp is required", m.Name)
	}

	if err := validateLabels(m.Labels); err != nil {
		return fmt.Errorf("metric %q: %w", m.Name, err)
	}

	return nil
}

func validateLabels(labels Labels) error {
	_, hasContainerID := labels["container_id"]
	_, hasContainerName := labels["container_name"]
	if hasContainerID && !hasContainerName {
		return fmt.Errorf("label %q requires an accompanying %q label", "container_id", "container_name")
	}

	for k, v := range labels {
		if forbiddenLabelKeys[k] {
			return fmt.Errorf("label key %q is forbidden (unbounded cardinality)", k)
		}
		if k == "container_id" && len(v) > 12 {
			return fmt.Errorf("label %q must be truncated to 12 characters, got %d", k, len(v))
		}
		if net.ParseIP(v) != nil {
			return fmt.Errorf("label %q: value %q looks like a client IP address, which is forbidden", k, v)
		}
		if strings.Count(v, "/") >= 2 {
			return fmt.Errorf("label %q: value %q looks like a file path, which is forbidden", k, v)
		}
	}
	return nil
}
