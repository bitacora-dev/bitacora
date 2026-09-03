// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"encoding/json"
	"testing"
	"time"
)

func sampleInventory() Inventory {
	return Inventory{
		HostID:     "01J8X0000000000000000000",
		Kind:       InventoryShare,
		ReportedAt: time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC),
		Schema:     CurrentSchemaVersion,
		Items: []InventoryItem{
			{ID: "multimedia", Name: "Multimedia", Attrs: Labels{"path": "/mnt/user/multimedia", "mode": "private", "protocol": "smb"}},
		},
	}
}

func TestInventory_MarshalsWithADRFieldNames(t *testing.T) {
	encoded, err := json.Marshal(sampleInventory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, field := range []string{"host_id", "kind", "reported_at", "schema", "items"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("expected JSON field %q, got keys %v", field, decoded)
		}
	}
	if decoded["kind"] != "share" {
		t.Errorf("expected kind %q, got %v", "share", decoded["kind"])
	}
}

func TestInventory_ValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(inv *Inventory)
		wantErr bool
	}{
		{"valid inventory", func(inv *Inventory) {}, false},
		{"valid with no items", func(inv *Inventory) { inv.Items = nil }, false},
		{"missing host_id", func(inv *Inventory) { inv.HostID = "" }, true},
		{"missing kind", func(inv *Inventory) { inv.Kind = "" }, true},
		{"missing reported_at", func(inv *Inventory) { inv.ReportedAt = time.Time{} }, true},
		{"missing schema", func(inv *Inventory) { inv.Schema = 0 }, true},
		{"item missing id", func(inv *Inventory) { inv.Items[0].ID = "" }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := sampleInventory()
			tc.mutate(&inv)
			err := inv.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestInventory_UnknownKindIsAcceptedNotRejected(t *testing.T) {
	inv := sampleInventory()
	inv.Kind = InventoryKind("something_a_future_ADR_adds")
	if err := inv.Validate(); err != nil {
		t.Fatalf("expected an unknown-but-present kind to validate, got: %v", err)
	}
}
