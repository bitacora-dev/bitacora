//go:build !linux

package resourcebudget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

type unsupportedEventSink struct{ events []schema.Event }

func (s *unsupportedEventSink) Event(event schema.Event) {
	s.events = append(s.events, event)
}

func TestSample_ReturnsUnsupportedOutsideLinux(t *testing.T) {
	_, _, err := Sample(1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Sample error = %v, want ErrUnsupported", err)
	}
}

func TestMonitor_RunDoesNotEmitUnmeasuredBudgetEvent(t *testing.T) {
	sink := &unsupportedEventSink{}
	err := (&Monitor{HostID: "host-a", Sink: sink}).Run(context.Background(), 1, time.Second)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Run error = %v, want ErrUnsupported", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("emitted %d events without a measurement", len(sink.events))
	}
}
