package schema

import (
	"fmt"
	"time"
)

// LogLine is one line of raw log output. Logs are the raw material; events
// are what's been understood from them (ADR-0006). A LogLine only becomes
// an Event if an extraction rule promotes it.
type LogLine struct {
	TS              time.Time
	HostID          string
	Source          string
	Stream          string
	UnitOrContainer string
	Level           string
	PID             int
	Message         string
}

// Validate enforces the minimal required fields.
func (l LogLine) Validate() error {
	if l.HostID == "" {
		return fmt.Errorf("log line: host_id is required")
	}
	if l.Source == "" {
		return fmt.Errorf("log line: source is required")
	}
	if l.TS.IsZero() {
		return fmt.Errorf("log line: ts is required")
	}
	return nil
}
