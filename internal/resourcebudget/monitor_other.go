//go:build !linux

package resourcebudget

import (
	"context"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// EventSink is the narrow boundary the budget monitor needs to make a budget
// breach visible in the agent timeline.
type EventSink interface {
	Event(event schema.Event)
}

// Monitor has no measurement implementation outside Linux. It is retained so
// callers can start consistently on every supported build target.
type Monitor struct {
	HostID string
	Sink   EventSink
}

// Run reports that no resource budget can be measured on this platform. It
// deliberately emits no budget event: no observed measurement exists.
func (Monitor) Run(context.Context, int, time.Duration) error {
	return ErrUnsupported
}
