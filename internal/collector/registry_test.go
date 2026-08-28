package collector

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubCollector struct {
	name     string
	requires []Capability
	initErr  error
}

func (c *stubCollector) Name() string                                  { return c.name }
func (c *stubCollector) Requires() []Capability                        { return c.requires }
func (c *stubCollector) Init(context.Context, Config, *HostInfo) error { return c.initErr }
func (c *stubCollector) Collect(context.Context, Sink) error           { return nil }
func (c *stubCollector) Close() error                                  { return nil }

func TestRegistry_ResolveDisablesOnMissingCapability(t *testing.T) {
	reg := &Registry{}
	reg.Register(&stubCollector{name: "needs-docker", requires: []Capability{"docker"}}, time.Second, time.Second)

	regs, disabled := reg.Resolve(context.Background(), Config{}, &HostInfo{}, map[Capability]bool{})

	if len(regs) != 0 {
		t.Fatalf("expected 0 registrations, got %d", len(regs))
	}
	if len(disabled) != 1 || disabled[0].Name != "needs-docker" {
		t.Fatalf("expected needs-docker to be disabled, got %+v", disabled)
	}
}

func TestRegistry_ResolveRegistersWhenCapabilityPresentAndInitSucceeds(t *testing.T) {
	reg := &Registry{}
	reg.Register(&stubCollector{name: "cpu"}, 10*time.Second, time.Second)

	regs, disabled := reg.Resolve(context.Background(), Config{}, &HostInfo{}, map[Capability]bool{})

	if len(disabled) != 0 {
		t.Fatalf("expected no disabled collectors, got %+v", disabled)
	}
	if len(regs) != 1 || regs[0].Collector.Name() != "cpu" {
		t.Fatalf("expected cpu to be registered, got %+v", regs)
	}
}

func TestRegistry_ResolveDisablesOnInitError(t *testing.T) {
	reg := &Registry{}
	reg.Register(&stubCollector{name: "broken", initErr: errors.New("bad config")}, time.Second, time.Second)

	regs, disabled := reg.Resolve(context.Background(), Config{}, &HostInfo{}, map[Capability]bool{})

	if len(regs) != 0 {
		t.Fatalf("expected 0 registrations, got %d", len(regs))
	}
	if len(disabled) != 1 || disabled[0].Name != "broken" {
		t.Fatalf("expected broken to be disabled, got %+v", disabled)
	}
}
