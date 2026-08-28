// Package schema defines the canonical data model shared by every piece of
// Bitácora: metrics, events and log lines (ADR-0006), and the host_id that
// ties them all together (ADR-0004). Nothing that stores or transports data
// should invent its own shape — it uses these types.
package schema

// CurrentSchemaVersion is the schema version stamped on new records.
// Compatible changes (adding an optional field) don't bump it; breaking
// changes do, and require an explicit migration.
const CurrentSchemaVersion = 1

// Severity is an event's severity level.
type Severity string

// Valid Severity values, per ADR-0006.
const (
	SeverityDebug    Severity = "debug"
	SeverityInfo     Severity = "info"
	SeverityNotice   Severity = "notice"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

func (s Severity) valid() bool {
	switch s {
	case SeverityDebug, SeverityInfo, SeverityNotice, SeverityWarn, SeverityError, SeverityCritical:
		return true
	default:
		return false
	}
}

// Labels are metric/event label pairs. Keys and values are plain strings;
// see Metric.Validate for the cardinality and forbidden-label rules.
type Labels map[string]string

// LogRef anchors an Event back to the log block and line that produced it.
type LogRef struct {
	BlockID string `json:"block_id"`
	Line    int    `json:"line"`
}

// EventSubject identifies what an Event is about.
type EventSubject struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	PID  int    `json:"pid,omitempty"`
}
