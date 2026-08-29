package schema

import (
	"fmt"
	"time"
)

// InventoryKind identifies what an Inventory snapshot lists. New kinds are
// added as the project's surface grows (ADR-0015, ADR-0016, ADR-0017) —
// this isn't a closed set enforced by Validate, since a future kind
// shouldn't require a schema package release to become valid.
type InventoryKind string

// Known InventoryKind values, documented for discoverability. Consumers
// should tolerate an unknown kind (a newer agent talking to an older hub,
// or vice versa) rather than reject it.
const (
	InventoryShare            InventoryKind = "share"
	InventoryVM               InventoryKind = "vm"
	InventoryUser             InventoryKind = "user"
	InventoryVPNTunnel        InventoryKind = "vpn_tunnel"
	InventoryUPS              InventoryKind = "ups"
	InventoryHardwareIdentity InventoryKind = "hardware_identity"
	InventoryCPUTopology      InventoryKind = "cpu_topology"
	InventoryPackageUpdate    InventoryKind = "package_update"
)

// InventoryItem is one entry of an Inventory snapshot — one share, one VM,
// one user, one outdated package. Attrs is free-form per Kind, same
// philosophy as Job.Stats (ADR-0010): canonical keys when they exist,
// free otherwise.
type InventoryItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Attrs Labels `json:"attrs,omitempty"`
}

// Inventory is a declarative snapshot of list-shaped data (ADR-0015): the
// fourth canonical data shape alongside Metric, Event and LogLine, for
// data that's neither a time series nor a discrete occurrence. Resent in
// full each time it's collected — a receiver replaces its stored copy for
// (HostID, Kind), it never appends, same as the capability manifest
// (ADR-0004).
type Inventory struct {
	HostID     string          `json:"host_id"`
	Kind       InventoryKind   `json:"kind"`
	ReportedAt time.Time       `json:"reported_at"`
	Schema     int             `json:"schema"`
	Items      []InventoryItem `json:"items"`
}

// Validate enforces the required fields. Items may be empty — an empty
// share list on a host with no shares configured is a valid, meaningful
// snapshot, not an error.
func (inv Inventory) Validate() error {
	if inv.HostID == "" {
		return fmt.Errorf("inventory: host_id is required")
	}
	if inv.Kind == "" {
		return fmt.Errorf("inventory %q: kind is required", inv.HostID)
	}
	if inv.ReportedAt.IsZero() {
		return fmt.Errorf("inventory %q/%q: reported_at is required", inv.HostID, inv.Kind)
	}
	if inv.Schema < 1 {
		return fmt.Errorf("inventory %q/%q: schema must be >= 1", inv.HostID, inv.Kind)
	}
	for i, item := range inv.Items {
		if item.ID == "" {
			return fmt.Errorf("inventory %q/%q: item %d: id is required", inv.HostID, inv.Kind, i)
		}
	}
	return nil
}
